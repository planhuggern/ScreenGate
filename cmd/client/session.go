package main

import "encoding/binary"

func sessionInfoError(info []byte, sessionID uint32) string {
	if len(info) < 20 {
		return "truncated_response"
	}
	read := binary.LittleEndian.Uint32
	if read(info[0:4]) != 1 {
		return "unknown_level"
	}
	if read(info[8:12]) != sessionID {
		return "session_mismatch"
	}
	state, flags := read(info[12:16]), read(info[16:20])
	if state > 9 {
		return "unknown_session_state"
	}
	if state == 0 && flags != 0 && flags != 1 {
		return "unknown_lock_flag"
	}
	return ""
}

// wtsSessionUnlocked reads the prefix of WTSINFOEXW level 1. The Data union
// starts at byte 8 (after Level and four alignment bytes), including on Windows
// x86. Its first fields are SessionId, SessionState and SessionFlags.
// https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/ns-wtsapi32-wtsinfoex_level1_w
func wtsSessionUnlocked(info []byte, sessionID uint32) bool {
	if len(info) < 20 {
		return false
	}
	read := binary.LittleEndian.Uint32
	return read(info[0:4]) == 1 && // Level 1
		read(info[8:12]) == sessionID &&
		read(info[12:16]) == 0 && // WTSActive
		read(info[16:20]) == 1 // WTS_SESSIONSTATE_UNLOCK; locked is 0
}
