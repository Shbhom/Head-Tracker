//go:build darwin && cgo

// Package native contains the macOS Objective-C++ Bluetooth bridge.
package native

/*
#cgo darwin CFLAGS: -I${SRCDIR}
#cgo darwin CXXFLAGS: -x objective-c++ -std=c++17
#cgo darwin LDFLAGS: -framework IOBluetooth -framework Foundation

#include "buds_bluetooth.h"
*/
import "C"

// FindConnectedBuds2Pro returns the address and name of a connected Galaxy
// Buds2 Pro known to macOS. If IOBluetooth does not report a match, it checks
// System Information before concluding that the device is disconnected.
// Like the transport, this requires the main event loop provided by osx.Run.
func FindConnectedBuds2Pro() (address, name string, found bool, err error) {
	var addressBuffer [256]C.char
	var nameBuffer [256]C.char

	if C.buds_find_connected_buds2pro(
		&addressBuffer[0], C.size_t(len(addressBuffer)),
		&nameBuffer[0], C.size_t(len(nameBuffer)),
	) == 0 {
		return findConnectedBuds2ProFromSystemProfiler()
	}

	return C.GoString(&addressBuffer[0]), C.GoString(&nameBuffer[0]), true, nil
}
