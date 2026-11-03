// Package opentrack exports orientation to opentrack's UDP receiver.
package opentrack

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shbhom/buds_head_tracker/pkg/protocol"
)

// Packet normalizes q and encodes six little-endian float64 values:
// x/y/z translation in cm (zero), then ZYX yaw/pitch/roll in degrees.
// Physical Buds axes have not yet been calibrated; adjust source axes and
// inversion in opentrack after checking each movement independently.
func Packet(q protocol.Quaternion) ([48]byte, error) {
	var packet [48]byte
	n := math.Sqrt(q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W)
	if n < 1e-9 || math.IsNaN(n) || math.IsInf(n, 0) {
		return packet, fmt.Errorf("invalid quaternion norm")
	}
	x, y, z, w := q.X/n, q.Y/n, q.Z/n, q.W/n
	angles := [3]float64{
		math.Atan2(2*(w*z+x*y), 1-2*(y*y+z*z)),
		math.Asin(math.Max(-1, math.Min(1, 2*(w*y-z*x)))),
		math.Atan2(2*(w*x+y*z), 1-2*(x*x+y*y)),
	}
	for i, angle := range angles {
		binary.LittleEndian.PutUint64(packet[(i+3)*8:], math.Float64bits(angle*180/math.Pi))
	}
	return packet, nil
}

// Converter is used by a single sensor event loop. Map entries specify the raw
// Euler source for output yaw,pitch,roll, optionally prefixed with '-'.
type Converter struct {
	axes      [3]int
	signs     [3]float64
	center    bool
	reference *protocol.Quaternion
}

func NewConverter(mapping string, center bool) (*Converter, error) {
	parts := strings.Split(mapping, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("mapping needs three sources for yaw,pitch,roll")
	}
	c := &Converter{center: center}
	used := [3]bool{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		c.signs[i] = 1
		if strings.HasPrefix(part, "-") {
			c.signs[i] = -1
			part = part[1:]
		}
		axis := -1
		for j, name := range []string{"yaw", "pitch", "roll"} {
			if part == name {
				axis = j
			}
		}
		if axis < 0 || used[axis] {
			return nil, fmt.Errorf("mapping must use yaw, pitch, roll once each (optional '-' inverts)")
		}
		used[axis] = true
		c.axes[i] = axis
	}
	return c, nil
}

// Reset captures a new neutral pose on the next valid sample.
func (c *Converter) Reset() { c.reference = nil }

func (c *Converter) Packet(q protocol.Quaternion) ([48]byte, error) {
	packet, err := Packet(q)
	if err != nil {
		return packet, err
	}
	if c.center {
		n := math.Sqrt(q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W)
		q = protocol.Quaternion{X: q.X / n, Y: q.Y / n, Z: q.Z / n, W: q.W / n}
		if c.reference == nil {
			ref := q
			c.reference = &ref
		}
		// inverse(reference) * current: express motion in the neutral sensor frame
		r := c.reference
		q = protocol.Quaternion{
			X: r.W*q.X - r.X*q.W - r.Y*q.Z + r.Z*q.Y,
			Y: r.W*q.Y + r.X*q.Z - r.Y*q.W - r.Z*q.X,
			Z: r.W*q.Z - r.X*q.Y + r.Y*q.X - r.Z*q.W,
			W: r.W*q.W + r.X*q.X + r.Y*q.Y + r.Z*q.Z,
		}
		packet, err = Packet(q)
		if err != nil {
			return packet, err
		}
	}
	raw := packet
	for i, axis := range c.axes {
		v := math.Float64frombits(binary.LittleEndian.Uint64(raw[(axis+3)*8:])) * c.signs[i]
		binary.LittleEndian.PutUint64(packet[(i+3)*8:], math.Float64bits(v))
	}
	return packet, nil
}

type Sender struct {
	conn        *net.UDPConn
	destination *net.UDPAddr
	converter   *Converter
	recenter    atomic.Bool
}

func Dial(address string) (*Sender, error) {
	return DialMapped(address, "yaw,pitch,roll", false)
}

func DialMapped(address, mapping string, center bool) (*Sender, error) {
	converter, err := NewConverter(mapping, center)
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	// An unconnected socket avoids treating asynchronous ICMP "port
	// unreachable" responses as the failure of a persistent UDP connection.
	// Delivery remains best-effort: the receiver need not be running.
	network := "udp4"
	if addr.IP != nil && addr.IP.To4() == nil {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, destination: addr, converter: converter}, nil
}

func (s *Sender) Close() error { return s.conn.Close() }

// Recenter requests neutral capture on the next valid sample. It is safe to
// call from a UI/input goroutine while the sensor loop calls Send.
func (s *Sender) Recenter() { s.recenter.Store(true) }

func (s *Sender) Send(q protocol.Quaternion) error {
	if s.recenter.Swap(false) {
		s.converter.center = true
		s.converter.Reset()
	}
	packet, err := s.converter.Packet(q)
	if err != nil {
		return err
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		return err
	}
	_, err = s.conn.WriteToUDP(packet[:], s.destination)
	return err
}
