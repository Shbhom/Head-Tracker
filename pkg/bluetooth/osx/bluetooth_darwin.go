//go:build darwin && cgo

// Package osx provides the macOS Bluetooth transport. Call Run from main on
// the initial OS thread (locked in main's init) to service IOBluetooth events.
package osx

import (
	"context"
	"github.com/shbhom/buds_head_tracker/pkg/bluetooth/osx/native"
)

type Connection = native.Connection

func Connect(address, serviceUUID string) (*Connection, error) {
	return native.Connect(address, serviceUUID)
}

func ConnectContext(ctx context.Context, address, serviceUUID string) (*Connection, error) {
	return native.ConnectContext(ctx, address, serviceUUID)
}

// Run pumps the macOS main run loop until work returns. All connections must
// be closed by work before it returns. It must run on the initial OS thread.
func Run(work func() error) error {
	done := make(chan error, 1)
	go func() { done <- work() }()
	for {
		select {
		case err := <-done:
			return err
		default:
			native.PumpMainLoop()
		}
	}
}
