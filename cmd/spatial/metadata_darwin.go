//go:build darwin && cgo

package main

/*
#cgo darwin LDFLAGS: -Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/Info.plist
*/
import "C"
