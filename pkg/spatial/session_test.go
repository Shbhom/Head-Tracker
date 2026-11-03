package spatial

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/shbhom/buds_head_tracker/pkg/protocol"
)

type mockTransport struct {
	mu        sync.Mutex
	writes    []protocol.Frame
	read      chan []byte
	sent      chan protocol.Frame
	closed    bool
	failWrite int
}

func newMock() *mockTransport {
	return &mockTransport{read: make(chan []byte, 64), sent: make(chan protocol.Frame, 16)}
}
func (m *mockTransport) Read(ctx context.Context, p []byte) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case b, ok := <-m.read:
		if !ok {
			return 0, io.EOF
		}
		return copy(p, b), nil
	}
}
func (m *mockTransport) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var parser protocol.Parser
	frames := parser.Feed(p)
	if len(frames) != 1 {
		return 0, errors.New("invalid outgoing packet")
	}
	m.writes = append(m.writes, frames[0])
	m.sent <- frames[0]
	if m.failWrite == len(m.writes) {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func (m *mockTransport) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true; return nil }

func TestSessionStreamsAndCleansUp(t *testing.T) {
	m := newMock()
	ack, _ := protocol.Encode(protocol.Frame{ID: protocol.SpatialAudioControl, Payload: []byte{protocol.AttachSuccess}})
	q, _ := protocol.Encode(protocol.Frame{ID: protocol.SpatialAudioData, Payload: []byte{protocol.BudGrv, 0, 0, 0, 0, 0, 0, 0x10, 0x27, 0}})
	m.read <- ack
	for _, b := range q {
		m.read <- []byte{b}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	err := Run(ctx, m, Options{OnEvent: func(e Event) error {
		count++
		if e.Quaternion == nil || e.Quaternion.W != 1 || e.Sequence != 1 || e.Timestamp.IsZero() {
			t.Errorf("bad event: %+v", e)
		}
		cancel()
		return nil
	}})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	checkCleanup(t, m, 4)
}

func checkCleanup(t *testing.T, m *mockTransport, count int) {
	t.Helper()
	if !m.closed || len(m.writes) != count {
		t.Fatalf("closed=%v writes=%d", m.closed, len(m.writes))
	}
	last := m.writes[len(m.writes)-2:]
	if last[0].ID != protocol.SpatialAudioControl || last[0].Payload[0] != protocol.Detach || last[1].ID != protocol.SetSpatialAudio || last[1].Payload[0] != 0 {
		t.Fatalf("cleanup = %+v", last)
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	m := newMock()
	err := Run(context.Background(), m, Options{IdleTimeout: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("expected idle timeout")
	}
	checkCleanup(t, m, 4)
}

func TestSessionAttachFailure(t *testing.T) {
	m := newMock()
	m.failWrite = 2
	if err := Run(context.Background(), m, Options{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err=%v", err)
	}
	checkCleanup(t, m, 4)
}

func TestSessionRemoteDisconnect(t *testing.T) {
	m := newMock()
	close(m.read)
	if err := Run(context.Background(), m, Options{}); !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v", err)
	}
	checkCleanup(t, m, 4)
}

func TestSessionKeepAlive(t *testing.T) {
	m := newMock()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, m, Options{}) }()
	var gotKeepAlive bool
	for !gotKeepAlive {
		select {
		case frame := <-m.sent:
			gotKeepAlive = frame.ID == protocol.SpatialAudioControl && frame.Payload[0] == protocol.KeepAlive
		case <-ctx.Done():
			t.Fatal("no keep-alive")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	checkCleanup(t, m, 5)
}
