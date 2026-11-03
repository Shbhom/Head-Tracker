// Package spatial manages a head-orientation session independently of Bluetooth.
package spatial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/shbhom/buds_head_tracker/pkg/protocol"
)

type Transport interface {
	Read(context.Context, []byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Sequence  uint64    `json:"sequence"`
	protocol.SpatialEvent
}

type Options struct {
	// IdleTimeout bounds the wait for the first/next quaternion. Zero uses 15s.
	IdleTimeout time.Duration
	OnEvent     func(Event) error
	Logf        func(string, ...any)
}

// Run owns t, enables spatial streaming, and detaches/disables on every exit.
// It does not change calibration, firmware, or other earbud settings.
func Run(ctx context.Context, t Transport, options Options) (runErr error) {
	defer func() { runErr = errors.Join(runErr, t.Close()) }()
	if options.IdleTimeout == 0 {
		options.IdleTimeout = 15 * time.Second
	}
	if options.IdleTimeout < 0 {
		return errors.New("idle timeout must be positive")
	}
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	send := func(id, value byte) error {
		packet, err := protocol.Encode(protocol.Frame{ID: id, Payload: []byte{value}})
		if err != nil {
			return err
		}
		n, err := t.Write(packet)
		if err == nil && n != len(packet) {
			err = io.ErrShortWrite
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Attempt both cleanup commands even if enable/attach only partially succeeds.
	defer func() {
		for _, command := range [][2]byte{{protocol.SpatialAudioControl, protocol.Detach}, {protocol.SetSpatialAudio, 0}} {
			if err := send(command[0], command[1]); err != nil {
				logf("spatial cleanup: %v", err)
				runErr = errors.Join(runErr, fmt.Errorf("spatial cleanup: %w", err))
			}
		}
	}()
	if err := send(protocol.SetSpatialAudio, 1); err != nil {
		return fmt.Errorf("enable spatial: %w", err)
	}
	if err := send(protocol.SpatialAudioControl, protocol.Attach); err != nil {
		return fmt.Errorf("attach sensor: %w", err)
	}
	logf("Spatial enable and attach sent; waiting for sensor data")

	readCtx, cancel := context.WithCancel(ctx)
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult, 16)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			buffer := make([]byte, 4096)
			n, err := t.Read(readCtx, buffer)
			select {
			case reads <- readResult{buffer[:n], err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); <-readerDone }()
	keepAlive := time.NewTicker(2 * time.Second)
	defer keepAlive.Stop()
	idle := time.NewTimer(options.IdleTimeout)
	defer idle.Stop()
	var parser protocol.Parser
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return fmt.Errorf("no quaternion received for %s; wear both buds, close competing clients, and check firmware support", options.IdleTimeout)
		case <-keepAlive.C:
			if err := send(protocol.SpatialAudioControl, protocol.KeepAlive); err != nil {
				return fmt.Errorf("keep-alive: %w", err)
			}
		case read := <-reads:
			invalid := parser.Invalid
			for _, frame := range parser.Feed(read.data) {
				switch frame.ID {
				case protocol.SpatialAudioControl:
					if frame.Fragment || len(frame.Payload) == 0 {
						logf("Invalid spatial control response")
						continue
					}
					logf("Spatial control response: %d (attach success=2, detach success=3)", frame.Payload[0])
				case protocol.SpatialAudioData:
					event, err := protocol.DecodeSpatial(frame)
					if err != nil {
						logf("Invalid spatial event: %v", err)
						continue
					}
					if event.Quaternion != nil {
						idle.Reset(options.IdleTimeout)
					}
					sequence++
					if options.OnEvent != nil {
						if err := options.OnEvent(Event{Timestamp: time.Now().UTC(), Sequence: sequence, SpatialEvent: event}); err != nil {
							return err
						}
					}
				}
			}
			if parser.Invalid != invalid {
				logf("SPP parser: discarded=%d CRC failures=%d", parser.Invalid, parser.CRCFailures)
			}
			if read.err != nil {
				return fmt.Errorf("Bluetooth read: %w", read.err)
			}
		}
	}
}
