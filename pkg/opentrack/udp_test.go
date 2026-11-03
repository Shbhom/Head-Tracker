package opentrack

import (
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"

	"github.com/shbhom/buds_head_tracker/pkg/protocol"
)

func TestPacket(t *testing.T) {
	s := math.Sqrt(0.5)
	for _, tc := range []struct {
		name string
		q    protocol.Quaternion
		want [6]float64
	}{
		{"identity", protocol.Quaternion{W: 2}, [6]float64{}},
		{"yaw", protocol.Quaternion{Z: s, W: s}, [6]float64{0, 0, 0, 90, 0, 0}},
		{"pitch", protocol.Quaternion{Y: 0.5, W: math.Sqrt(0.75)}, [6]float64{0, 0, 0, 0, 60, 0}},
		{"roll", protocol.Quaternion{X: -s, W: s}, [6]float64{0, 0, 0, 0, 0, -90}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet, err := Packet(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range tc.want {
				got := math.Float64frombits(binary.LittleEndian.Uint64(packet[i*8:]))
				if math.Abs(got-want) > 1e-5 {
					t.Fatalf("axis %d: got %g want %g", i, got, want)
				}
			}
		})
	}
	for _, q := range []protocol.Quaternion{{}, {W: math.NaN()}, {X: math.Inf(1)}} {
		if _, err := Packet(q); err == nil {
			t.Fatal("accepted invalid quaternion")
		}
	}
}

func TestUDP(t *testing.T) {
	receiver, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := Dial(receiver.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := sender.Send(protocol.Quaternion{W: 1}); err != nil {
		t.Fatal(err)
	}
	receiver.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 100)
	n, _, err := receiver.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 48 {
		t.Fatalf("got %d bytes, want 48", n)
	}
	// Recenter can be requested independently of the sensor event loop. Even
	// a non-identity pose must be exported as zero on the next valid sample.
	done := make(chan struct{})
	go func() { sender.Recenter(); close(done) }()
	<-done
	if err := sender.Send(protocol.Quaternion{X: 0.3, Y: 0.4, Z: 0.5, W: 0.6}); err != nil {
		t.Fatal(err)
	}
	n, _, err = receiver.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 48 {
		t.Fatalf("got %d bytes", n)
	}
	for i := 0; i < 6; i++ {
		if math.Abs(math.Float64frombits(binary.LittleEndian.Uint64(buf[i*8:]))) > 1e-6 {
			t.Fatal("recenter did not export zero")
		}
	}
}

func TestMappingAndCalibration(t *testing.T) {
	for _, mapping := range []string{"", "yaw,pitch", "yaw,yaw,roll", "x,pitch,roll"} {
		if _, err := NewConverter(mapping, true); err == nil {
			t.Fatalf("accepted %q", mapping)
		}
	}
	c, err := NewConverter("-roll,pitch,yaw", true)
	if err != nil {
		t.Fatal(err)
	}
	s := math.Sqrt(0.5)
	// Neutral is a 90 degree yaw. Then turn 30 degrees around local X.
	ref := protocol.Quaternion{Z: s, W: s}
	p, err := c.Packet(ref)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if math.Abs(math.Float64frombits(binary.LittleEndian.Uint64(p[i*8:]))) > 1e-6 {
			t.Fatal("neutral was not zero")
		}
	}
	a, b := math.Sin(math.Pi/12), math.Cos(math.Pi/12)
	p, err = c.Packet(protocol.Quaternion{X: s * a, Y: s * a, Z: s * b, W: s * b})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{-30, 0, 0} {
		got := math.Float64frombits(binary.LittleEndian.Uint64(p[(i+3)*8:]))
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("axis %d: got %g want %g", i, got, want)
		}
	}
	c.Reset()
	p, err = c.Packet(ref)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(math.Float64frombits(binary.LittleEndian.Uint64(p[24:]))) > 1e-6 {
		t.Fatal("reset failed")
	}
}

func TestReceiverRestart(t *testing.T) {
	// Reserve an ephemeral loopback port, then stop listening before sending.
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := receiver.LocalAddr().(*net.UDPAddr)
	receiver.Close()
	sender, err := Dial(addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	for cycle := 0; cycle < 2; cycle++ {
		for i := 0; i < 10; i++ {
			if err := sender.Send(protocol.Quaternion{W: 1}); err != nil {
				t.Fatalf("no listener: %v", err)
			}
		}
		receiver, err = net.ListenUDP("udp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		if err := sender.Send(protocol.Quaternion{W: 1}); err != nil {
			receiver.Close()
			t.Fatal(err)
		}
		receiver.SetReadDeadline(time.Now().Add(time.Second))
		var buf [100]byte
		n, _, readErr := receiver.ReadFromUDP(buf[:])
		receiver.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if n != 48 {
			t.Fatalf("got %d bytes, want 48", n)
		}
	}
}
