package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	Attach                byte = 0
	Detach                byte = 1
	AttachSuccess         byte = 2
	DetachSuccess         byte = 3
	KeepAlive             byte = 4
	BudGrv                byte = 32
	WearOn                byte = 33
	WearOff               byte = 34
	BudGyrocal            byte = 35
	BudSensorStuck        byte = 36
	SensorSupported       byte = 37
	GyroBiasExistence     byte = 38
	ManualGyrocalReady    byte = 39
	ManualGyrocalNotReady byte = 40
	BudGyrocalFail        byte = 41
)

type Quaternion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
	W float64 `json:"w"`
}

type SpatialEvent struct {
	ID         byte        `json:"event_id"`
	Quaternion *Quaternion `json:"quaternion,omitempty"`
	// The ninth GRV byte's meaning is not established; preserve it unchanged.
	Data []byte `json:"data"`
}

func DecodeSpatial(f Frame) (SpatialEvent, error) {
	if f.ID != SpatialAudioData || f.Fragment {
		return SpatialEvent{}, fmt.Errorf("not an unfragmented spatial-data message")
	}
	if len(f.Payload) == 0 {
		return SpatialEvent{}, fmt.Errorf("empty spatial event")
	}
	e := SpatialEvent{ID: f.Payload[0], Data: append([]byte(nil), f.Payload[1:]...)}
	minimum := 0
	switch e.ID {
	case BudGrv:
		minimum = 9
	case BudGyrocal:
		minimum = 6
	case WearOn, WearOff, BudSensorStuck:
		minimum = 1
	}
	if len(e.Data) < minimum {
		return SpatialEvent{}, fmt.Errorf("spatial event %d: need at least %d data bytes, got %d", e.ID, minimum, len(e.Data))
	}
	if e.ID == BudGrv {
		var v [4]float64
		for i := range v {
			v[i] = float64(int16(binary.LittleEndian.Uint16(e.Data[i*2:]))) / 10000
		}
		e.Quaternion = &Quaternion{X: v[0], Y: v[1], Z: v[2], W: v[3]}
	}
	return e, nil
}
