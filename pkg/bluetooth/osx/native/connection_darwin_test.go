//go:build darwin && cgo

package native

import (
	"context"
	"errors"
	"testing"
)

func TestConnectAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection, err := ConnectContext(ctx, "unused", "unused")
	if connection != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("connection=%v err=%v", connection, err)
	}
}
