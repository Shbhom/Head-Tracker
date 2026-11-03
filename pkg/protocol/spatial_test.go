package protocol

import "testing"

func TestDecodeQuaternion(t *testing.T) {
	var p Parser
	f := p.Feed(fromHex(t, "fd0d00c22018fcd00748f4282300fec1dd"))[0]
	e, err := DecodeSpatial(f)
	if err != nil {
		t.Fatal(err)
	}
	want := Quaternion{X: -0.1, Y: 0.2, Z: -0.3, W: 0.9}
	if e.Quaternion == nil || *e.Quaternion != want {
		t.Fatalf("quaternion = %+v", e.Quaternion)
	}
	if len(e.Data) != 9 || e.Data[8] != 0 {
		t.Fatal("raw sensor flag lost")
	}
}

func TestSpatialEvents(t *testing.T) {
	for _, tt := range []struct {
		id      byte
		minimum int
	}{
		{BudGrv, 9}, {WearOn, 1}, {WearOff, 1}, {BudGyrocal, 6}, {BudSensorStuck, 1},
		{SensorSupported, 0}, {GyroBiasExistence, 0}, {ManualGyrocalReady, 0},
		{ManualGyrocalNotReady, 0}, {BudGyrocalFail, 0}, {255, 0},
	} {
		payload := append([]byte{tt.id}, make([]byte, tt.minimum)...)
		event, err := DecodeSpatial(Frame{ID: SpatialAudioData, Payload: payload})
		if err != nil || event.ID != tt.id {
			t.Fatalf("event %d: %v", tt.id, err)
		}
		if tt.minimum > 0 {
			if _, err := DecodeSpatial(Frame{ID: SpatialAudioData, Payload: payload[:len(payload)-1]}); err == nil {
				t.Fatalf("event %d accepted short payload", tt.id)
			}
		}
	}
	for _, frame := range []Frame{{ID: SpatialAudioData}, {ID: SpatialAudioControl, Payload: []byte{2}}, {ID: SpatialAudioData, Payload: []byte{SensorSupported}, Fragment: true}} {
		if _, err := DecodeSpatial(frame); err == nil {
			t.Fatal("accepted invalid spatial frame")
		}
	}
}
