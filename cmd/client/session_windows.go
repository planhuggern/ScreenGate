//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	wmWTSSessionChange = 0x02B1
	wtsSessionLogon    = 0x5
	wtsSessionUnlock   = 0x8
	notifyThisSession  = 0
)

type windowsPoint struct {
	X int32
	Y int32
}

type windowsMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   windowsPoint
	Private uint32
}

var (
	createWindowEx                = syscall.NewLazyDLL("user32.dll").NewProc("CreateWindowExW")
	getMessage                    = syscall.NewLazyDLL("user32.dll").NewProc("GetMessageW")
	translateMessage              = syscall.NewLazyDLL("user32.dll").NewProc("TranslateMessage")
	dispatchMessage               = syscall.NewLazyDLL("user32.dll").NewProc("DispatchMessageW")
	registerSessionNotification   = syscall.NewLazyDLL("wtsapi32.dll").NewProc("WTSRegisterSessionNotification")
	unregisterSessionNotification = syscall.NewLazyDLL("wtsapi32.dll").NewProc("WTSUnRegisterSessionNotification")
)

func startSessionEvents() <-chan string {
	events := make(chan string, 1)
	go func() {
		className, _ := syscall.UTF16PtrFromString("STATIC")
		window, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
		if window == 0 {
			close(events)
			return
		}
		if ok, _, _ := registerSessionNotification.Call(window, notifyThisSession); ok == 0 {
			close(events)
			return
		}
		defer unregisterSessionNotification.Call(window)

		var message windowsMessage
		for {
			result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			if message.Message == wmWTSSessionChange {
				var event string
				switch message.WParam {
				case wtsSessionLogon:
					event = "logon"
				case wtsSessionUnlock:
					event = "unlock"
				}
				if event != "" {
					select {
					case events <- event:
					default:
					}
				}
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&message)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	return events
}
