//go:build darwin && cgo

package native

import "testing"

func TestParseBluetoothReport(t *testing.T) {
	tests := []struct {
		name    string
		report  string
		address string
		device  string
		found   bool
		wantErr bool
	}{
		{
			name:    "connected buds",
			report:  `{"SPBluetoothDataType":[{"device_connected":[{"Galaxy Buds2 Pro":{"device_address":"AA:BB:CC:DD:EE:FF"}}]}]}`,
			address: "AA:BB:CC:DD:EE:FF",
			device:  "Galaxy Buds2 Pro",
			found:   true,
		},
		{
			name:    "model identifier case insensitive",
			report:  `{"SPBluetoothDataType":[{"device_connected":[{"sm-r510":{"device_address":"AA:BB"}}]}]}`,
			address: "AA:BB",
			device:  "sm-r510",
			found:   true,
		},
		{
			name:   "paired but disconnected",
			report: `{"SPBluetoothDataType":[{"device_not_connected":[{"Galaxy Buds2 Pro":{"device_address":"AA:BB"}}]}]}`,
		},
		{
			name:   "unrelated connected device",
			report: `{"SPBluetoothDataType":[{"device_connected":[{"Echo Dot":{"device_address":"AA:BB"}}]}]}`,
		},
		{name: "no connected devices", report: `{"SPBluetoothDataType":[{}]}`},
		{name: "missing report", report: `{}`, wantErr: true},
		{name: "empty report", report: `{"SPBluetoothDataType":[]}`, wantErr: true},
		{name: "invalid json", report: `invalid`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			address, name, found, err := parseBluetoothReport([]byte(tt.report))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if address != tt.address || name != tt.device || found != tt.found {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", address, name, found, tt.address, tt.device, tt.found)
			}
		})
	}
}
