// Package protocol implements the Buds2 Pro SPP wire format without cgo.
package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	ServiceUUID              = "2e73a4ad-332d-41fc-90e2-16bef06523f2"
	SetSpatialAudio     byte = 124
	SpatialAudioData    byte = 194
	SpatialAudioControl byte = 195
	maxSize                  = 0x3ff
)

type Frame struct {
	ID      byte
	Payload []byte
	// These are raw wire flags. Upstream encode/decode disagree on the name
	// of bit 12, so do not infer request/response semantics from incoming data.
	Flag12   bool
	Fragment bool
}

// CRC16 is CRC-16/XMODEM: polynomial 0x1021, initial value zero, no reflection.
// Buds transmit it little-endian, over message ID + payload (not the header).
func CRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func Encode(f Frame) ([]byte, error) {
	size := len(f.Payload) + 3
	if size > maxSize {
		return nil, fmt.Errorf("SPP payload too large: %d", len(f.Payload))
	}
	header := uint16(size)
	if f.Flag12 {
		header |= 0x1000
	}
	if f.Fragment {
		header |= 0x2000
	}
	b := make([]byte, size+4)
	b[0] = 0xfd
	binary.LittleEndian.PutUint16(b[1:3], header)
	b[3] = f.ID
	copy(b[4:], f.Payload)
	binary.LittleEndian.PutUint16(b[len(b)-3:], CRC16(b[3:len(b)-3]))
	b[len(b)-1] = 0xdd
	return b, nil
}

// Parser retains incomplete frames between calls. Invalid bytes and bad CRCs
// are skipped one byte at a time to recover later frames. Fragment flags are
// exposed, not reassembled; spatial decoding rejects fragmented messages.
type Parser struct {
	buffer      []byte
	Invalid     uint64
	CRCFailures uint64
}

func (p *Parser) Feed(data []byte) []Frame {
	var frames []Frame
	// Process incrementally so even a very large input cannot grow the retained
	// buffer without bound. A wire frame is at most 1027 bytes.
	for _, b := range data {
		p.buffer = append(p.buffer, b)
		for len(p.buffer) > 0 {
			i := bytes.IndexByte(p.buffer, 0xfd)
			if i < 0 {
				p.Invalid += uint64(len(p.buffer))
				p.buffer = p.buffer[:0]
				break
			}
			p.Invalid += uint64(i)
			p.buffer = p.buffer[i:]
			if len(p.buffer) < 3 {
				break
			}
			header := binary.LittleEndian.Uint16(p.buffer[1:3])
			size := int(header & maxSize)
			// Captured Buds2 Pro frames set additional header bits (10, 14,
			// 15). Their meaning is unknown; they are not grounds to reject
			// an otherwise valid length/end marker/CRC.
			if size < 3 {
				p.Invalid++
				p.buffer = p.buffer[1:]
				continue
			}
			total := size + 4
			if len(p.buffer) < total {
				break
			}
			packet := p.buffer[:total]
			if packet[total-1] != 0xdd {
				p.Invalid++
				p.buffer = p.buffer[1:]
				continue
			}
			if CRC16(packet[3:total-3]) != binary.LittleEndian.Uint16(packet[total-3:total-1]) {
				p.Invalid++
				p.CRCFailures++
				p.buffer = p.buffer[1:]
				continue
			}
			frames = append(frames, Frame{ID: packet[3], Payload: append([]byte(nil), packet[4:total-3]...), Flag12: header&0x1000 != 0, Fragment: header&0x2000 != 0})
			p.buffer = p.buffer[total:]
		}
	}
	return frames
}
