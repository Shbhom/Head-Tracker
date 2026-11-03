//go:build darwin && cgo

package native

/*
#include <stdlib.h>
#include "buds_bluetooth.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unsafe"
)

type Connection struct {
	mu        sync.Mutex
	ptr       *C.buds_connection
	ChannelID int
	MTU       int
}

// Connect requires the initial macOS thread to be pumping PumpMainLoop.
func Connect(address, uuid string) (*Connection, error) {
	return ConnectContext(context.Background(), address, uuid)
}

// ConnectContext keeps native opening asynchronous so the main run loop remains
// free to deliver callbacks. It bounds the handshake to 15 seconds.
func ConnectContext(ctx context.Context, address, uuid string) (*Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, u := C.CString(address), C.CString(uuid)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(u))
	var diagnostic [256]C.char
	ptr := C.buds_connect(a, u, nil, nil, nil)
	if ptr == nil {
		return nil, errors.New("Bluetooth connection could not be allocated")
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := C.buds_connection_status(ptr, &diagnostic[0], C.size_t(len(diagnostic)))
		if state == C.BUDS_STATE_CONNECTED {
			return &Connection{ptr: ptr, ChannelID: int(C.buds_channel_id(ptr)), MTU: int(C.buds_outgoing_mtu(ptr))}, nil
		}
		if state == C.BUDS_STATE_ERROR || state == C.BUDS_STATE_CLOSED {
			C.buds_disconnect(ptr)
			return nil, fmt.Errorf("Bluetooth connect: %s", C.GoString(&diagnostic[0]))
		}
		select {
		case <-ctx.Done():
			C.buds_disconnect(ptr)
			return nil, ctx.Err()
		case <-deadline.C:
			C.buds_disconnect(ptr)
			return nil, fmt.Errorf("Bluetooth connect timed out: %s; fully quit GalaxyBudsClient and disconnect/reconnect the Buds to clear a stale channel", C.GoString(&diagnostic[0]))
		case <-ticker.C:
		}
	}
}

// PumpMainLoop must be called on the initial, locked OS thread.
func PumpMainLoop() { C.buds_pump_main_loop(0.02) }

func (c *Connection) Read(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		c.mu.Lock()
		if c.ptr == nil {
			c.mu.Unlock()
			return 0, io.EOF
		}
		n := int(C.buds_read(c.ptr, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p))))
		c.mu.Unlock()
		if n > 0 {
			return n, nil
		}
		if n == -1 {
			return 0, io.EOF
		}
		if n < -1 {
			return 0, errors.New("Bluetooth receive buffer overflow; stream cannot be trusted")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Connection) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		return 0, io.ErrClosedPipe
	}
	result := C.buds_write(c.ptr, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
	if result != 0 {
		return 0, fmt.Errorf("RFCOMM write failed (%d); some bytes may have been sent", int(result))
	}
	return len(p), nil
}

func (c *Connection) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		C.buds_disconnect(c.ptr)
		c.ptr = nil
	}
	return nil
}
