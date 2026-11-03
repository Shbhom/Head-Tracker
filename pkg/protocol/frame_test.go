package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func fromHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCRC16(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x31c3 {
		t.Fatalf("CRC = %04x", got)
	}
}

func TestEncodeReferenceVectors(t *testing.T) {
	// Synthetic vectors calculated independently with Python binascii.crc_hqx,
	// not hardware captures and not an encoder/decoder round-trip oracle.
	for _, tt := range []struct {
		id, value byte
		hex       string
	}{
		{SetSpatialAudio, 1, "fd04007c01155ddd"},
		{SpatialAudioControl, Attach, "fd0400c3000743dd"},
		{SpatialAudioControl, KeepAlive, "fd0400c3048303dd"},
	} {
		b, err := Encode(Frame{ID: tt.id, Payload: []byte{tt.value}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, fromHex(t, tt.hex)) {
			t.Errorf("got %x, want %s", b, tt.hex)
		}
	}
	if _, err := Encode(Frame{Payload: make([]byte, 1021)}); err == nil {
		t.Fatal("oversize accepted")
	}
}

func TestParserChunkBoundaries(t *testing.T) {
	packet := fromHex(t, "fd0d00c22018fcd00748f4282300fec1dd")
	for split := 0; split <= len(packet); split++ {
		var p Parser
		frames := p.Feed(packet[:split])
		frames = append(frames, p.Feed(packet[split:])...)
		if len(frames) != 1 || frames[0].ID != SpatialAudioData {
			t.Fatalf("split %d: %v", split, frames)
		}
		if p.Invalid != 0 {
			t.Fatalf("split %d discarded valid bytes", split)
		}
	}
}

func TestParserRecoveryAndFlags(t *testing.T) {
	good := fromHex(t, "fd0400c3000743dd")
	badCRC := bytes.Clone(good)
	badCRC[4] ^= 1
	badEnd := bytes.Clone(good)
	badEnd[len(badEnd)-1] = 0
	flagged, _ := Encode(Frame{ID: SpatialAudioData, Payload: []byte{BudGrv}, Flag12: true, Fragment: true})
	stream := []byte{0, 1, 2, 0xfd, 0, 0}
	stream = append(stream, badCRC...)
	stream = append(stream, badEnd...)
	stream = append(stream, good...)
	stream = append(stream, flagged...)
	var p Parser
	frames := p.Feed(stream)
	if len(frames) != 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	if !frames[1].Flag12 || !frames[1].Fragment {
		t.Fatal("lost header flags")
	}
	if p.CRCFailures != 1 || p.Invalid == 0 {
		t.Fatalf("parser stats: %+v", p)
	}
}

func TestCapturedHeaderBits(t *testing.T) {
	// Actual spatial packet from session.jsonl, with valid CRC 0x7700.
	packet := fromHex(t, "fd2950c2209a1cf9ee701407008fb61500a37857005700890010ad47f150529ff316f48501c8000000000077dd")
	for _, flags := range []uint16{0x1000, 0x5000, 0x9000, 0xd000, 0x1400, 0x5400, 0x9400, 0xd400} {
		b := bytes.Clone(packet)
		b[1] = 0x29 | byte(flags)
		b[2] = byte(flags >> 8)
		var parser Parser
		frames := parser.Feed(b)
		if len(frames) != 1 || parser.Invalid != 0 || parser.CRCFailures != 0 {
			t.Fatalf("flags=%04x frames=%d invalid=%d CRC=%d", flags, len(frames), parser.Invalid, parser.CRCFailures)
		}
	}
}

func TestParserBoundedIncompleteFrame(t *testing.T) {
	var p Parser
	p.Feed([]byte{0xfd, 0xff, 0x03})
	p.Feed(make([]byte, 100000))
	if len(p.buffer) > maxSize+4 {
		t.Fatalf("unbounded buffer: %d", len(p.buffer))
	}
}

func FuzzParser(f *testing.F) {
	f.Add([]byte{0xfd, 4, 0, 0xc3, 0, 7, 0x43, 0xdd})
	f.Fuzz(func(t *testing.T, b []byte) {
		var p Parser
		for _, frame := range p.Feed(b) {
			if len(frame.Payload) > maxSize-3 {
				t.Fatal("oversize frame")
			}
		}
		if len(p.buffer) > maxSize+4 {
			t.Fatal("unbounded buffer")
		}
	})
}
