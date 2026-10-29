//go:build windows

package main

import (
	"errors"
	"syscall"
	"testing"
)

func TestSessionCallFailureKeepsOnlyAvailableWindowsCodes(t *testing.T) {
	for _, err := range []error{nil, syscall.Errno(0), errors.New("no Windows code")} {
		if got := sessionCallFailure("OpenInputDesktop", err); got.Code != nil {
			t.Fatal("an absent Windows code must remain absent")
		}
	}
	got := sessionCallFailure("OpenInputDesktop", syscall.Errno(5))
	if got.Code == nil || *got.Code != 5 || got.Call != "OpenInputDesktop" {
		t.Fatal("available Windows error code was lost")
	}
}
