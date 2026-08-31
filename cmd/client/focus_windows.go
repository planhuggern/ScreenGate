//go:build windows

package main

import (
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"
)

const processQueryLimitedInformation = 0x1000

var (
	foregroundWindow          = syscall.NewLazyDLL("user32.dll").NewProc("GetForegroundWindow")
	windowThreadProcessID     = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowThreadProcessId")
	openProcess               = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	queryFullProcessImageName = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
	closeHandle               = syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")
)

func foregroundApp() (string, error) {
	window, _, _ := foregroundWindow.Call()
	if window == 0 {
		return "", errors.New("no foreground window")
	}

	var processID uint32
	windowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&processID)))
	if processID == 0 {
		return "", errors.New("no foreground process")
	}

	handle, _, _ := openProcess.Call(processQueryLimitedInformation, 0, uintptr(processID))
	if handle == 0 {
		return "", errors.New("cannot open foreground process")
	}
	defer closeHandle.Call(handle)

	path := make([]uint16, 32768)
	size := uint32(len(path))
	ok, _, _ := queryFullProcessImageName.Call(handle, 0, uintptr(unsafe.Pointer(&path[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return "", errors.New("cannot read foreground process")
	}
	return filepath.Base(syscall.UTF16ToString(path[:size])), nil
}
