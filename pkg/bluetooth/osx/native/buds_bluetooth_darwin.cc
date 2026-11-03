// Go tracks .cc sources directly. CXXFLAGS select Objective-C++ compilation.
#import <Foundation/Foundation.h>
#import <IOBluetooth/IOBluetooth.h>
#import <dispatch/dispatch.h>

#include "buds_bluetooth.h"
#include <algorithm>
#include <cstdio>
#include <deque>

// Connection objects and delegates are accessed only on the main thread.
// Never hold a mutex across IOBluetooth calls: they can invoke delegates inline.
static void on_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) { block(); }
    else {
        // Do not execute the connection wait inside a main dispatch-queue
        // block. A nested run loop cannot drain that same queue recursively,
        // which starves IOBluetooth's SDP/open-completion callbacks.
        dispatch_semaphore_t done = dispatch_semaphore_create(0);
        CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopDefaultMode, ^{
            block();
            dispatch_semaphore_signal(done);
        });
        CFRunLoopWakeUp(CFRunLoopGetMain());
        dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
        dispatch_release(done);
    }
}

@class BudsRFCOMMDelegate;
struct buds_connection {
    IOBluetoothDevice *device = nil;
    IOBluetoothRFCOMMChannel *channel = nil;
    BudsRFCOMMDelegate *delegate = nil;
    buds_data_callback onData = nullptr;
    buds_state_callback onState = nullptr;
    void *context = nullptr;
    bool closed = false;
    bool overflow = false;
    int channelID = 0;
    int state = BUDS_STATE_CONNECTING;
    char message[256] = "Starting Bluetooth connection";
    IOBluetoothSDPUUID *uuid = nil;
    CFAbsoluteTime sdpStarted = 0;
    std::deque<uint8_t> received;
};

static void notify_state(buds_connection *c, int state, const char *message) {
    c->state = state;
    snprintf(c->message, sizeof(c->message), "%s", message);
    if (c->onState) c->onState(state, message, c->context);
}

static void start_channel(buds_connection *c);

static bool is_buds2pro_name(NSString *name) {
    NSString *lower = [name lowercaseString];
    return [lower containsString:@"buds2 pro"] || [lower containsString:@"sm-r510"];
}

extern "C" void buds_list_devices(buds_device_callback callback, void *context) {
    if (!callback) return;
    on_main(^{ @autoreleasepool {
        for (IOBluetoothDevice *device in [IOBluetoothDevice pairedDevices]) {
            callback([[device name] ?: @"Unknown Bluetooth device" UTF8String],
                     [[device addressString] ?: @"" UTF8String],
                     [device isConnected] ? 1 : 0, context);
        }
    }});
}

static int find_connected_buds2pro(char *address, size_t address_capacity,
                                            char *name, size_t name_capacity) {
    if (!address || !address_capacity || !name || !name_capacity) return 0;
    address[0] = name[0] = '\0';
    @autoreleasepool {
        for (IOBluetoothDevice *device in [IOBluetoothDevice pairedDevices]) {
            if (![device isConnected] || !is_buds2pro_name([device name])) continue;
            snprintf(address, address_capacity, "%s", [[device addressString] ?: @"" UTF8String]);
            snprintf(name, name_capacity, "%s", [[device name] ?: @"" UTF8String]);
            return 1;
        }
    }
    return 0;
}

extern "C" int buds_find_connected_buds2pro(char *address, size_t address_capacity,
                                            char *name, size_t name_capacity) {
    __block int found = 0;
    on_main(^{ found = find_connected_buds2pro(address, address_capacity, name, name_capacity); });
    return found;
}

@interface BudsRFCOMMDelegate : NSObject <IOBluetoothRFCOMMChannelDelegate>
@property(nonatomic, assign) buds_connection *connection;
@end

@implementation BudsRFCOMMDelegate
- (void)rfcommChannelOpenComplete:(IOBluetoothRFCOMMChannel *)channel status:(IOReturn)status {
    buds_connection *c = self.connection;
    if (!c || c->closed) return;
    if (status == kIOReturnSuccess && [channel isOpen]) {
        notify_state(c, BUDS_STATE_CONNECTED, "RFCOMM connection established");
    } else {
        char message[256];
        snprintf(message, sizeof(message), "RFCOMM channel %u open completion failed (IOError 0x%08x); quit competing Bluetooth clients", c->channelID, status);
        notify_state(c, BUDS_STATE_ERROR, message);
    }
}
- (void)rfcommChannelData:(IOBluetoothRFCOMMChannel *)channel data:(void *)data length:(size_t)length {
    (void)channel;
    buds_connection *c = self.connection;
    if (!c || c->closed || !data || !length) return;
    if (c->onData) {
        c->onData(static_cast<const uint8_t *>(data), length, c->context);
    } else {
        // Fail explicitly rather than dropping bytes and corrupting the stream.
        if (c->received.size() + length > 1024 * 1024) {
            c->overflow = true;
            notify_state(c, BUDS_STATE_ERROR, "RFCOMM receive buffer overflow");
            return;
        }
        const uint8_t *bytes = static_cast<const uint8_t *>(data);
        c->received.insert(c->received.end(), bytes, bytes + length);
    }
}
- (void)rfcommChannelClosed:(IOBluetoothRFCOMMChannel *)channel {
    (void)channel;
    buds_connection *c = self.connection;
    if (!c || c->closed) return;
    c->closed = true;
    notify_state(c, BUDS_STATE_CLOSED, "RFCOMM channel closed");
}
@end

extern "C" void buds_pump_main_loop(double seconds) {
    @autoreleasepool {
        [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:seconds]];
    }
}

static void dispose(buds_connection *c) {
    c->closed = true;
    c->delegate.connection = nullptr;
    [c->channel setDelegate:nil];
    [c->channel closeChannel];
    [c->channel release];
    [c->device release];
    [c->uuid release];
    [c->delegate release];
    delete c;
}

extern "C" buds_connection *buds_connect(const char *address, const char *service_uuid,
    buds_data_callback on_data, buds_state_callback on_state, void *context) {
    if (!address || !service_uuid) return nullptr;
    __block buds_connection *result = nullptr;
    on_main(^{ @autoreleasepool {
        buds_connection *c = new buds_connection{};
        c->onData = on_data; c->onState = on_state; c->context = context;
        c->delegate = [[BudsRFCOMMDelegate alloc] init];
        c->delegate.connection = c;
        notify_state(c, BUDS_STATE_CONNECTING, "Opening RFCOMM connection");

        NSUUID *parsed = [[[NSUUID alloc] initWithUUIDString:[NSString stringWithUTF8String:service_uuid]] autorelease];
        if (!parsed) {
            notify_state(c, BUDS_STATE_ERROR, "Invalid RFCOMM service UUID"); result = c; return;
        }
        uuid_t bytes;
        [parsed getUUIDBytes:bytes];
        IOBluetoothSDPUUID *uuid = [IOBluetoothSDPUUID uuidWithBytes:bytes length:16];
        c->uuid = [uuid retain];
        c->device = [[IOBluetoothDevice deviceWithAddressString:[NSString stringWithUTF8String:address]] retain];
        if (!c->device) {
            notify_state(c, BUDS_STATE_ERROR, "Device unavailable; check pairing and Bluetooth permission"); result = c; return;
        }
        if (![c->device isConnected]) {
            IOReturn status = [c->device openConnection];
            if (status != kIOReturnSuccess) {
                char message[192];
                snprintf(message, sizeof(message), "Cannot open Bluetooth base connection (IOError 0x%08x); check device availability and Bluetooth permission", status);
                notify_state(c, BUDS_STATE_ERROR, message); result = c; return;
            }
        }

        // Query all services: UUID-filtered SDP queries can fail on Ventura+.
        notify_state(c, BUDS_STATE_CONNECTING, "Waiting for Bluetooth SDP service records");
        c->sdpStarted = CFAbsoluteTimeGetCurrent();
        // Do not retain a completion target indefinitely: some macOS versions
        // never deliver SDP completions. Poll the service records instead.
        IOReturn status = [c->device performSDPQuery:nil];
        if (status != kIOReturnSuccess) {
            // A cached service record can still be usable after a query error.
            start_channel(c);
        }
        result = c;
    }});
    return result;
}

static void start_channel(buds_connection *c) {
    if (c->channelID || c->closed || c->state == BUDS_STATE_ERROR) return;
    IOBluetoothSDPServiceRecord *record = [c->device getServiceRecordForUUID:c->uuid];
    BluetoothRFCOMMChannelID channelID = 0;
    if (!record || [record getRFCOMMChannelID:&channelID] != kIOReturnSuccess) {
        notify_state(c, BUDS_STATE_ERROR, "Buds2 Pro SPP service unavailable; check permission or another active client"); return;
    }
    c->channelID = channelID;
    char message[256];
    snprintf(message, sizeof(message), "Waiting for RFCOMM channel %u open completion", channelID);
    notify_state(c, BUDS_STATE_CONNECTING, message);
    IOBluetoothRFCOMMChannel *channel = nil;
    IOReturn status = [c->device openRFCOMMChannelAsync:&channel withChannelID:channelID delegate:c->delegate];
    // Apple's open API returns an owned channel; dispose releases it.
    c->channel = channel;
    if (channel && [channel isOpen] && !c->closed) {
        notify_state(c, BUDS_STATE_CONNECTED, "RFCOMM connection established");
    } else if (status != kIOReturnSuccess || !channel) {
        snprintf(message, sizeof(message), "RFCOMM channel %u could not start (IOError 0x%08x); quit competing Bluetooth clients", channelID, status);
        notify_state(c, BUDS_STATE_ERROR, message);
    }
}

extern "C" int buds_connection_status(buds_connection *c, char *message, size_t capacity) {
    __block int state = BUDS_STATE_ERROR;
    on_main(^{
        // Some macOS versions refresh SDP records but never invoke the
        // completion delegate. Use the refreshed/cached record after a short
        // grace period instead of making that callback a hard requirement.
        if (c->state == BUDS_STATE_CONNECTING && !c->channelID &&
            c->sdpStarted && CFAbsoluteTimeGetCurrent() - c->sdpStarted >= 2 &&
            [c->device getServiceRecordForUUID:c->uuid]) start_channel(c);
        if (c->state == BUDS_STATE_CONNECTING && c->channel && [c->channel isOpen] && !c->closed)
            notify_state(c, BUDS_STATE_CONNECTED, "RFCOMM connection established");
        state = c->state;
        if (message && capacity) snprintf(message, capacity, "%s", c->message);
    });
    return state;
}

extern "C" int buds_write(buds_connection *c, const uint8_t *data, size_t length) {
    if (!c || !data || !length) return -1;
    __block int result = 0;
    on_main(^{ @autoreleasepool {
        if (c->closed || ![c->channel isOpen]) { result = -2; return; }
        BluetoothRFCOMMMTU mtu = [c->channel getMTU];
        if (!mtu) { result = -3; return; }
        for (size_t offset = 0; offset < length;) {
            UInt16 chunk = static_cast<UInt16>(std::min(static_cast<size_t>(mtu), length-offset));
            if ([c->channel writeSync:(void *)(data+offset) length:chunk] != kIOReturnSuccess) {
                result = -3; return;
            }
            offset += chunk;
        }
    }});
    return result;
}

extern "C" int buds_read(buds_connection *c, uint8_t *data, size_t capacity) {
    if (!c || !data || !capacity) return -1;
    __block int count = 0;
    on_main(^{
        if (c->overflow) { count = -2; return; }
        size_t n = std::min(capacity, c->received.size());
        for (size_t i = 0; i < n; i++) { data[i] = c->received.front(); c->received.pop_front(); }
        count = static_cast<int>(n);
        if (!count && c->closed) count = -1;
    });
    return count;
}

extern "C" void buds_disconnect(buds_connection *c) {
    if (c) on_main(^{ @autoreleasepool { dispose(c); }});
}
extern "C" size_t buds_outgoing_mtu(const buds_connection *c) {
    __block size_t mtu = 0;
    if (c) on_main(^{ mtu = [c->channel getMTU]; });
    return mtu;
}
extern "C" int buds_channel_id(const buds_connection *c) {
    return c ? c->channelID : 0;
}
