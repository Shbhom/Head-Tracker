package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shbhom/buds_head_tracker/pkg/protocol"
	"github.com/shbhom/buds_head_tracker/pkg/spatial"
)

func testServer(t *testing.T, config Config) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(s)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		ts.Close()
	})
	return s, ts
}

func dial(t *testing.T, ts *httptest.Server, headers http.Header, subprotocols []string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(ts.URL, "https")+"/ws/orientation", &websocket.DialOptions{HTTPClient: ts.Client(), HTTPHeader: headers, Subprotocols: subprotocols})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readMessage(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWSSBroadcastAndSnapshot(t *testing.T) {
	s, ts := testServer(t, Config{})
	a, b := dial(t, ts, nil, nil), dial(t, ts, nil, nil)
	if readMessage(t, a).Status != "starting" || readMessage(t, b).Status != "starting" {
		t.Fatal("missing initial status")
	}
	event := spatial.Event{Timestamp: time.Now().UTC(), Sequence: 42, SpatialEvent: protocol.SpatialEvent{Quaternion: &protocol.Quaternion{X: 0.1, Y: 0.2, Z: 0.3, W: 0.9}}}
	s.Publish(event)
	for _, c := range []*websocket.Conn{a, b} {
		if readMessage(t, c).Status != "streaming" {
			t.Fatal("missing streaming status")
		}
		m := readMessage(t, c)
		if m.Type != "orientation" || m.Sequence != 42 || m.Quaternion == nil || *m.Quaternion != *event.Quaternion || !m.Timestamp.Equal(event.Timestamp) {
			t.Fatalf("message=%+v", m)
		}
	}
	late := dial(t, ts, nil, nil)
	if readMessage(t, late).Status != "streaming" || readMessage(t, late).Sequence != 42 {
		t.Fatal("late client did not receive snapshot")
	}
	// A disconnected client must not interrupt the publisher/other clients.
	a.CloseNow()
	s.SetStatus("error", "Bluetooth disconnected")
	if m := readMessage(t, b); m.Status != "error" || m.Message != "Bluetooth disconnected" {
		t.Fatalf("error status=%+v", m)
	}
	resp, err := ts.Client().Get(ts.URL + "/api/orientation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("stale orientation was exposed after disconnect")
	}
}

func TestAuthenticationAndOrigins(t *testing.T) {
	token := strings.Repeat("a", 64)
	_, ts := testServer(t, Config{Token: token, OriginPatterns: []string{"https://viewer.example"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, path := range []string{"/api/status", "/api/orientation", "/ws/orientation"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	c := dial(t, ts, header, nil)
	if readMessage(t, c).Type != "status" {
		t.Fatal("Bearer authentication failed")
	}
	browser := dial(t, ts, http.Header{"Origin": []string{"https://viewer.example"}}, []string{"buds.v1", "buds.auth." + token})
	if browser.Subprotocol() != "buds.v1" {
		t.Fatal("auth token must never be selected/echoed")
	}
	readMessage(t, browser)
	header.Set("Origin", "https://evil.example")
	bad, resp, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(ts.URL, "https")+"/ws/orientation", &websocket.DialOptions{HTTPClient: ts.Client(), HTTPHeader: header})
	if bad != nil {
		bad.CloseNow()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted origin accepted: %v", err)
	}
	resp, err = ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("health endpoint unavailable")
	}
}

func TestSlowSubscriberIsBounded(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.cancel()
	client := &subscriber{queue: make(chan []byte, 1), done: make(chan struct{})}
	s.clients[client] = struct{}{}
	s.SetStatus("connected", "")
	s.SetStatus("streaming", "")
	select {
	case <-client.done:
	default:
		t.Fatal("slow subscriber not disconnected")
	}
	if len(s.clients) != 0 || len(client.queue) != 1 {
		t.Fatal("subscriber queue grew")
	}
}

func TestConfiguration(t *testing.T) {
	for _, config := range []Config{
		{Address: "0.0.0.0:8080"}, {Address: ":8080", Token: strings.Repeat("a", 64)},
		{Address: "[::]:8080"}, {CertFile: "only-cert"}, {Token: "weak"},
		{Token: strings.Repeat("!", 64)}, {Address: "bad-address"},
	} {
		if s, err := New(config); err == nil {
			s.cancel()
			t.Fatalf("unsafe/invalid config accepted: %+v", config)
		}
	}
}

func TestStartWithTLSAndShutdown(t *testing.T) {
	// Use a trusted test certificate, but exercise the real Start/TLS listener.
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	defer fixture.Close()
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert := fixture.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Address: "127.0.0.1:0", CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	url, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: fixture.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	readMessage(t, c)
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("WebSocket survived shutdown")
	}
}

func TestCurrentOrientation(t *testing.T) {
	s, ts := testServer(t, Config{})
	s.Publish(spatial.Event{Timestamp: time.Now().UTC(), Sequence: 1, SpatialEvent: protocol.SpatialEvent{Quaternion: &protocol.Quaternion{W: 1}}})
	resp, err := ts.Client().Get(ts.URL + "/api/orientation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"w":1`) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}
