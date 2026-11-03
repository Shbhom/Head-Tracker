// Package api publishes head-orientation events over HTTP and WebSocket.
package api

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/shbhom/buds_head_tracker/pkg/protocol"
	"github.com/shbhom/buds_head_tracker/pkg/spatial"
)

type Config struct {
	Address  string
	CertFile string
	KeyFile  string
	Token    string
	// OriginPatterns is an explicit list of browser origins, e.g. localhost:3000.
	// Same-host origins are allowed automatically. No wildcard is added.
	OriginPatterns []string
}

type Message struct {
	Type       string               `json:"type"`
	Timestamp  time.Time            `json:"timestamp"`
	Sequence   uint64               `json:"sequence,omitempty"`
	Quaternion *protocol.Quaternion `json:"quaternion,omitempty"`
	Status     string               `json:"status,omitempty"`
	Message    string               `json:"message,omitempty"`
}

// Each client has a bounded queue. An overflowing client is disconnected rather
// than blocking acquisition, dropping status changes, or accumulating stale data.
type subscriber struct {
	queue chan []byte
	done  chan struct{}
}

type Server struct {
	config    Config
	tlsConfig *tls.Config
	http      *http.Server
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	clients   map[*subscriber]struct{}
	status    Message
	latest    *Message
	closed    bool
	wg        sync.WaitGroup
	errors    chan error
}

func New(config Config) (*Server, error) {
	if config.Address == "" {
		config.Address = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil {
		return nil, fmt.Errorf("API listen address: %w", err)
	}
	local := host == "localhost" || net.ParseIP(host).IsLoopback()
	if (config.CertFile == "") != (config.KeyFile == "") {
		return nil, errors.New("both TLS certificate and key are required")
	}
	if !local && (config.CertFile == "" || config.Token == "") {
		return nil, errors.New("non-loopback API binding requires TLS and BUDS_API_TOKEN")
	}
	if config.Token != "" {
		if len(config.Token) < 32 || len(config.Token) > 256 || strings.ContainsAny(config.Token, " \t\r\n,=+/:") {
			return nil, errors.New("API token must be 32–256 URL-safe characters (use openssl rand -hex 32)")
		}
		for _, ch := range config.Token {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return nil, errors.New("API token must contain only letters, digits, '-' or '_'")
			}
		}
	}
	var tlsConfig *tls.Config
	if config.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("API TLS certificate: %w", err)
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{config: config, tlsConfig: tlsConfig, ctx: ctx, cancel: cancel,
		clients: make(map[*subscriber]struct{}), errors: make(chan error, 1),
		status: Message{Type: "status", Timestamp: time.Now().UTC(), Status: "starting"}}
	s.http = &http.Server{Addr: config.Address, Handler: s, TLSConfig: tlsConfig,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	return s, nil
}

// Start binds before returning, so errors are reported before Bluetooth opens.
// It must be called once. Errors reports unexpected failures after startup.
func (s *Server) Start() (string, error) {
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return "", err
	}
	scheme := "ws"
	if s.tlsConfig != nil {
		listener = tls.NewListener(listener, s.tlsConfig)
		scheme = "wss"
	}
	go func() {
		err := s.http.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errors <- err
		}
	}()
	return scheme + "://" + listener.Addr().String() + "/ws/orientation", nil
}

func (s *Server) Errors() <-chan error { return s.errors }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/ws/orientation":
		s.stream(w, r)
	case "/api/status":
		s.mu.Lock()
		status := s.status
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, status)
	case "/api/orientation":
		s.mu.Lock()
		latest := s.latest
		s.mu.Unlock()
		if latest == nil {
			http.Error(w, "no current orientation", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, latest)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	if s.config.Token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.config.Token)) == 1 {
		return true
	}
	// Browsers cannot set Authorization on WebSocket upgrades. Supply the token
	// as an unselected subprotocol, never in the URL; only buds.v1 is echoed.
	if r.URL.Path == "/ws/orientation" {
		for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "buds.auth.") && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(p, "buds.auth.")), []byte(s.config.Token)) == 1 {
				return true
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) SetStatus(status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.status = Message{Type: "status", Timestamp: time.Now().UTC(), Status: status, Message: message}
	if status != "streaming" {
		s.latest = nil
	}
	s.broadcastLocked(s.status)
}

func (s *Server) Publish(event spatial.Event) {
	if event.Quaternion == nil {
		return
	}
	q := *event.Quaternion
	m := Message{Type: "orientation", Timestamp: event.Timestamp, Sequence: event.Sequence, Quaternion: &q}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.status.Status != "streaming" {
		s.status = Message{Type: "status", Timestamp: time.Now().UTC(), Status: "streaming"}
		s.broadcastLocked(s.status)
	}
	s.latest = &m
	s.broadcastLocked(m)
}

func (s *Server) broadcastLocked(m Message) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	for client := range s.clients {
		select {
		case client.queue <- b:
		default:
			close(client.done)
			delete(s.clients, client)
		}
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	client := &subscriber{queue: make(chan []byte, 16), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed || len(s.clients) >= 64 {
		s.mu.Unlock()
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	s.clients[client] = struct{}{}
	s.wg.Add(1)
	status, _ := json.Marshal(s.status)
	client.queue <- status
	if s.latest != nil {
		latest, _ := json.Marshal(s.latest)
		client.queue <- latest
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.clients, client); s.mu.Unlock(); s.wg.Done() }()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.config.OriginPatterns, Subprotocols: []string{"buds.v1"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024)
	// This is a server-to-client stream; CloseRead handles ping/pong and closes
	// clients that send application messages instead of accumulating input.
	ctx := conn.CloseRead(s.ctx)
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			_ = conn.Close(websocket.StatusPolicyViolation, "client is too slow; reconnect")
			return
		case b := <-client.queue:
			writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return
			}
		case <-heartbeat.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	err := s.http.Shutdown(ctx)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}
