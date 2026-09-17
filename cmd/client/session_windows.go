//go:build windows

package main

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	openInputDesktop         = syscall.NewLazyDLL("user32.dll").NewProc("OpenInputDesktop")
	closeDesktop             = syscall.NewLazyDLL("user32.dll").NewProc("CloseDesktop")
	getUserObjectInformation = syscall.NewLazyDLL("user32.dll").NewProc("GetUserObjectInformationW")
	getLastInputInfo         = syscall.NewLazyDLL("user32.dll").NewProc("GetLastInputInfo")
	getTickCount             = syscall.NewLazyDLL("kernel32.dll").NewProc("GetTickCount")
	processIDToSessionID     = syscall.NewLazyDLL("kernel32.dll").NewProc("ProcessIdToSessionId")
	querySessionInformation  = syscall.NewLazyDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
	freeSessionMemory        = syscall.NewLazyDLL("wtsapi32.dll").NewProc("WTSFreeMemory")
	createMutex              = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
)

func acquireClientMutex(identity string) (uintptr, error) {
	name, err := syscall.UTF16PtrFromString("Local\\ScreenGate-" + identity)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return 0, callErr
	}
	if callErr == syscall.Errno(183) {
		closeHandle.Call(handle)
		return 0, errors.New("ScreenGate is already running for this user and server")
	}
	return handle, nil
}

// Poll the actual session and input desktop rather than trusting notifications
// that can be missed during startup, sleep, fast-user switching, or remote use.
// No desktop-switching API is called here.
func currentSessionState(idleTimeout time.Duration) string {
	var sessionID uint32
	if ok, _, _ := processIDToSessionID.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&sessionID))); ok == 0 {
		return "locked"
	}
	var info *uint32
	var size uint32
	const wtsConnectState = 8
	ok, _, _ := querySessionInformation.Call(0, uintptr(sessionID), wtsConnectState, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)))
	if ok == 0 || info == nil {
		return "locked"
	}
	connected := size >= 4 && *info == 0 // WTSActive
	freeSessionMemory.Call(uintptr(unsafe.Pointer(info)))
	if !connected {
		return "locked"
	}
	const desktopReadObjects = 0x0001
	desktop, _, _ := openInputDesktop.Call(0, 0, desktopReadObjects)
	if desktop == 0 {
		return "locked"
	}
	defer closeDesktop.Call(desktop)
	var name [256]uint16
	var required uint32
	const userObjectName = 2
	ok, _, _ = getUserObjectInformation.Call(desktop, userObjectName, uintptr(unsafe.Pointer(&name[0])), unsafe.Sizeof(name), uintptr(unsafe.Pointer(&required)))
	if ok == 0 || !strings.EqualFold(syscall.UTF16ToString(name[:]), "Default") {
		return "locked"
	}
	if idleTimeout > 0 {
		info := struct {
			Size uint32
			Time uint32
		}{Size: 8}
		if ok, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); ok != 0 {
			ticks, _, _ := getTickCount.Call()
			// uint32 subtraction deliberately handles the 49.7-day tick rollover.
			if time.Duration(uint32(ticks)-info.Time)*time.Millisecond >= idleTimeout {
				return "idle"
			}
		}
	}
	return "active"
}
