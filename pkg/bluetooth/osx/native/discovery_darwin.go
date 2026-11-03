//go:build darwin && cgo

package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// system_profiler can report connected devices that IOBluetooth's pairedDevices
// lookup does not expose to a command-line process. This is discovery only: it
// does not grant permission to open a Bluetooth connection.
func findConnectedBuds2ProFromSystemProfiler() (address, name string, found bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "/usr/sbin/system_profiler", "SPBluetoothDataType", "-json").Output()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", "", false, fmt.Errorf("cannot determine Bluetooth connection status: %w", err)
	}
	return parseBluetoothReport(output)
}

func parseBluetoothReport(data []byte) (address, name string, found bool, err error) {
	var report struct {
		Bluetooth []struct {
			Connected []map[string]struct {
				Address string `json:"device_address"`
			} `json:"device_connected"`
		} `json:"SPBluetoothDataType"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return "", "", false, fmt.Errorf("cannot parse Bluetooth report: %w", err)
	}
	if len(report.Bluetooth) == 0 {
		return "", "", false, fmt.Errorf("macOS returned no Bluetooth report; connection status is unknown")
	}
	for _, controller := range report.Bluetooth {
		for _, devices := range controller.Connected {
			for name, device := range devices {
				lower := strings.ToLower(name)
				if strings.Contains(lower, "buds2 pro") || strings.Contains(lower, "sm-r510") {
					return device.Address, name, true, nil
				}
			}
		}
	}
	return "", "", false, nil
}
