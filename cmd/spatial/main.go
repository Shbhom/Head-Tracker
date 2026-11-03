//go:build darwin && cgo

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/shbhom/buds_head_tracker/pkg/api"
	"github.com/shbhom/buds_head_tracker/pkg/bluetooth/osx"
	"github.com/shbhom/buds_head_tracker/pkg/bluetooth/osx/native"
	"github.com/shbhom/buds_head_tracker/pkg/opentrack"
	"github.com/shbhom/buds_head_tracker/pkg/protocol"
	"github.com/shbhom/buds_head_tracker/pkg/spatial"
)

// macOS event delivery must stay on the process's initial OS thread.
func init() { runtime.LockOSThread() }

func main() {
	os.Exit(run())
}

func run() int {
	address := flag.String("address", "", "Buds Bluetooth address (default: discover connected Buds2 Pro)")
	duration := flag.Duration("duration", 0, "stop after this duration (zero: until Ctrl-C)")
	idle := flag.Duration("idle-timeout", 15*time.Second, "fail if no quaternion arrives within this interval")
	listen := flag.String("listen", "127.0.0.1:8080", "WebSocket/API listen address (empty disables the API)")
	cert := flag.String("tls-cert", "", "TLS certificate PEM file (enables wss)")
	key := flag.String("tls-key", "", "TLS private key PEM file")
	origins := flag.String("origins", "", "comma-separated allowed browser origins, e.g. https://localhost:3000")
	udpAddress := flag.String("opentrack", "", "opentrack UDP destination, e.g. 127.0.0.1:4242 (empty disables export)")
	udpMap := flag.String("opentrack-map", "yaw,pitch,roll", "raw sources for output yaw,pitch,roll; prefix '-' to invert (e.g. roll,pitch,yaw)")
	udpCenter := flag.Bool("opentrack-center", false, "use first valid quaternion as neutral pose; face forward when starting")
	flag.Parse()
	if *duration < 0 || *idle <= 0 {
		log.Fatal("duration must be nonnegative; idle-timeout must be positive")
	}
	if *address != "" {
		mac, err := net.ParseMAC(*address)
		if err != nil || len(mac) != 6 {
			log.Fatal("address must be a six-byte Bluetooth MAC address")
		}
		*address = mac.String()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	var server *api.Server
	if *listen != "" {
		var allowed []string
		for _, origin := range strings.Split(*origins, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				allowed = append(allowed, origin)
			}
		}
		var err error
		server, err = api.New(api.Config{Address: *listen, CertFile: *cert, KeyFile: *key, Token: os.Getenv("BUDS_API_TOKEN"), OriginPatterns: allowed})
		if err != nil {
			log.Print(err)
			return 1
		}
	} else if *cert != "" || *key != "" {
		log.Print("TLS flags require a listen address")
		return 1
	}
	var udp *opentrack.Sender
	if *udpAddress != "" {
		var err error
		udp, err = opentrack.DialMapped(*udpAddress, *udpMap, *udpCenter)
		if err != nil {
			log.Printf("opentrack: %v", err)
			return 1
		}
		defer udp.Close()
		log.Printf("opentrack UDP destination: %s (start its UDP receiver before tracking)", *udpAddress)
		log.Printf("opentrack axis map (yaw,pitch,roll): %s; startup neutral calibration: %t", *udpMap, *udpCenter)
		if *udpCenter {
			log.Print("Recenter: once streaming, face forward and press Enter in this terminal. The next sample becomes neutral.")
			go func() {
				// Stdin belongs to this command. EOF stops the control reader;
				// process exit releases a reader blocked waiting for a line.
				scanner := bufio.NewScanner(os.Stdin)
				for scanner.Scan() {
					if ctx.Err() != nil {
						return
					}
					udp.Recenter()
					log.Print("opentrack recenter requested; waiting for the next valid sensor sample")
				}
				if err := scanner.Err(); err != nil {
					log.Printf("recenter input: %v", err)
				}
			}()
		}
	}
	output := json.NewEncoder(os.Stdout)
	serverFailure := make(chan error, 1)
	watchDone := make(chan struct{})
	if server != nil {
		url, err := server.Start()
		if err != nil {
			log.Print(err)
			return 1
		}
		log.Printf("Orientation endpoint: %s", url)
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("API shutdown: %v", err)
			}
		}()
		go func() {
			defer close(watchDone)
			select {
			case err := <-server.Errors():
				serverFailure <- err
				cancelServer()
			case <-ctx.Done():
			}
		}()
	} else {
		close(watchDone)
	}
	var lastUDPWarning time.Time
	err := osx.Run(func() error {
		if server != nil {
			server.SetStatus("connecting", "Discovering and opening Galaxy Buds2 Pro")
		}
		if *address == "" {
			foundAddress, name, found, err := native.FindConnectedBuds2Pro()
			if err != nil {
				return err
			}
			if !found {
				return errors.New("no connected Galaxy Buds2 Pro found; connect them in macOS Bluetooth settings first")
			}
			*address = foundAddress
			log.Printf("Found %s (%s)", name, foundAddress)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Printf("Opening SPP service %s on %s", protocol.ServiceUUID, *address)
		connection, err := osx.ConnectContext(ctx, *address, protocol.ServiceUUID)
		if err != nil {
			return err
		}
		log.Printf("RFCOMM connected: channel=%d MTU=%d", connection.ChannelID, connection.MTU)
		if server != nil {
			server.SetStatus("connected", "Waiting for the first quaternion")
		}
		return spatial.Run(ctx, connection, spatial.Options{
			IdleTimeout: *idle, Logf: log.Printf,
			OnEvent: func(event spatial.Event) error {
				if server != nil {
					server.Publish(event)
				}
				if event.Quaternion == nil {
					log.Printf("Spatial sensor event=%d data=%x", event.ID, event.Data)
					return nil
				}
				if udp != nil {
					if err := udp.Send(*event.Quaternion); err != nil {
						// UDP output is optional and best-effort. Keep Bluetooth,
						// JSON and WebSocket streaming alive if the receiver stops.
						if time.Since(lastUDPWarning) >= 5*time.Second {
							log.Printf("opentrack UDP packet dropped (tracking continues): %v", err)
							lastUDPWarning = time.Now()
						}
					}
				}
				return output.Encode(event)
			},
		})
	})
	cancelServer()
	<-watchDone
	select {
	case serverErr := <-serverFailure:
		err = errors.Join(err, fmt.Errorf("API server: %w", serverErr))
	default:
	}
	if err != nil && !expectedStop(err) {
		if server != nil {
			server.SetStatus("error", err.Error())
		}
		log.Print(err)
		return 1
	}
	if server != nil {
		server.SetStatus("stopped", "Tracking stopped")
	}
	return 0
}

// A cancellation joined with a real cleanup error is still a failure.
func expectedStop(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !expectedStop(child) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
