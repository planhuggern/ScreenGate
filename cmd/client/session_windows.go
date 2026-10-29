//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"

	"screengate/internal/sessiondiag"
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

// Only failed calls contribute an error code. A zero/no-code result is left
// absent; successful Windows calls can leave a stale last-error value.
func sessionCallFailure(call string, err error) *sessiondiag.Failure {
	failure := &sessiondiag.Failure{Call: call, Reason: "call_failed"}
	if code, ok := err.(syscall.Errno); ok && code != 0 {
		value := uint32(code)
		failure.Code = &value
	}
	return failure
}

func currentSessionObservation(idleTimeout time.Duration) sessionObservation {
	return windowsSessionReader.observe(idleTimeout)
}

// Read the actual session and input desktop. No desktop-switching API is used.
var windowsSessionReader = sessionReader{
	sessionID: func() (uint32, *sessiondiag.Failure) {
		var id uint32
		if ok, _, err := processIDToSessionID.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&id))); ok == 0 {
			return 0, sessionCallFailure("ProcessIdToSessionId", err)
		}
		return id, nil
	},
	sessionInfo: func(id uint32) ([]byte, *sessiondiag.Failure) {
		var info *byte
		var size uint32
		const wtsSessionInfoEx = 25
		ok, _, err := querySessionInformation.Call(0, uintptr(id), wtsSessionInfoEx, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)))
		if info != nil {
			defer freeSessionMemory.Call(uintptr(unsafe.Pointer(info)))
		}
		if ok == 0 {
			return nil, sessionCallFailure("WTSQuerySessionInformationW", err)
		}
		if info == nil {
			return nil, &sessiondiag.Failure{Call: "WTSQuerySessionInformationW", Reason: "empty_response"}
		}
		// Only the prefix is needed. Copy it before releasing the WTS buffer.
		return append([]byte(nil), unsafe.Slice(info, int(min(size, 20)))...), nil
	},
	inputDesktop: func() (uintptr, *sessiondiag.Failure) {
		const desktopReadObjects = 0x0001
		desktop, _, err := openInputDesktop.Call(0, 0, desktopReadObjects)
		if desktop == 0 {
			return 0, sessionCallFailure("OpenInputDesktop", err)
		}
		return desktop, nil
	},
	closeDesktop: func(desktop uintptr) { closeDesktop.Call(desktop) },
	desktopName: func(desktop uintptr) (string, *sessiondiag.Failure) {
		var name [256]uint16
		var required uint32
		const userObjectName = 2
		ok, _, err := getUserObjectInformation.Call(desktop, userObjectName, uintptr(unsafe.Pointer(&name[0])), unsafe.Sizeof(name), uintptr(unsafe.Pointer(&required)))
		if ok == 0 {
			return "", sessionCallFailure("GetUserObjectInformationW", err)
		}
		return syscall.UTF16ToString(name[:]), nil
	},
	idleDuration: func() (time.Duration, *sessiondiag.Failure) {
		info := struct {
			Size uint32
			Time uint32
		}{Size: 8}
		if ok, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
			// This API documents only a zero/nonzero return value, not a
			// GetLastError code. Do not report a possibly stale thread error.
			// https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getlastinputinfo
			return 0, sessionCallFailure("GetLastInputInfo", nil)
		}
		ticks, _, _ := getTickCount.Call()
		// uint32 subtraction handles the 49.7-day tick rollover.
		return time.Duration(uint32(ticks)-info.Time) * time.Millisecond, nil
	},
}
