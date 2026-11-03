#ifndef BUDS_BLUETOOTH_H
#define BUDS_BLUETOOTH_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct buds_connection buds_connection;

typedef void (*buds_data_callback)(const uint8_t *data, size_t length, void *context);
typedef void (*buds_state_callback)(int state, const char *message, void *context);
typedef void (*buds_device_callback)(const char *name,
                                     const char *mac_address,
                                     int connected,
                                     void *context);

enum buds_connection_state {
    BUDS_STATE_CONNECTING = 0,
    BUDS_STATE_CONNECTED = 1,
    BUDS_STATE_CLOSED = 2,
    BUDS_STATE_ERROR = 3
};

/*
 * Enumerates paired devices known to macOS. The callback is invoked once for
 * every device. connected is non-zero when the device currently has an active
 * Bluetooth connection.
 */
void buds_list_devices(buds_device_callback callback, void *context);

/* Returns non-zero when a connected Galaxy Buds2 Pro is found. */
int buds_find_connected_buds2pro(char *mac_address_out,
                                 size_t mac_address_capacity,
                                 char *name_out,
                                 size_t name_capacity);

/*
 * Connects to a paired device and opens its RFCOMM service.
 *
 * service_uuid must be the Buds2 Pro SPP UUID. Opening is asynchronous; check
 * buds_connection_status and keep pumping the main run loop. A non-null handle
 * must be released even when the connection subsequently fails.
 *
 * The returned handle is owned by the caller and must be released with
 * buds_disconnect().
 */
buds_connection *buds_connect(const char *mac_address,
                              const char *service_uuid,
                              buds_data_callback on_data,
                              buds_state_callback on_state,
                              void *context);

/* Writes one logical payload. The implementation splits it to the RFCOMM MTU. */
int buds_write(buds_connection *connection, const uint8_t *data, size_t length);

/* Closes the RFCOMM channel and releases the connection handle. */
void buds_disconnect(buds_connection *connection);

/* Returns the negotiated outgoing RFCOMM MTU, or 0 if unavailable. */
size_t buds_outgoing_mtu(const buds_connection *connection);

/* All transport operations are marshalled to the macOS main thread. The host
 * must pump this loop on its initial, locked OS thread while using transport. */
void buds_pump_main_loop(double seconds);

/* Nonblocking receive: >0 bytes, 0 no data, -1 disconnected, -2 overflow.
 * Buffered bytes are delivered before a remote disconnect is reported. */
int buds_read(buds_connection *connection, uint8_t *data, size_t capacity);
int buds_channel_id(const buds_connection *connection);
int buds_connection_status(buds_connection *connection, char *message, size_t capacity);

#ifdef __cplusplus
}
#endif

#endif
