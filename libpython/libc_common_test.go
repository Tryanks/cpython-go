// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
)

func TestReadInterruptedByDeliveredSignal(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])

	tls := libc.NewTLS()
	defer tls.Close()
	buffer := make([]byte, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		noteSignalDelivery()
	}()

	if got := _ccgo_read(tls, int32(pipe[0]), uintptr(unsafe.Pointer(&buffer[0])), 1); got != -1 {
		t.Fatalf("read returned %d, want -1", got)
	}
	if got := syscall.Errno(*(*int32)(unsafe.Pointer(libc.X__errno_location(tls)))); got != syscall.EINTR {
		t.Fatalf("errno = %v, want EINTR", got)
	}
}

func TestWriteInterruptedByDeliveredSignal(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	if err := unix.SetNonblock(pipe[1], true); err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 4096)
	for {
		if _, err := unix.Write(pipe[1], block); err != nil {
			if err != syscall.EAGAIN {
				t.Fatal(err)
			}
			break
		}
	}
	if err := unix.SetNonblock(pipe[1], false); err != nil {
		t.Fatal(err)
	}

	tls := libc.NewTLS()
	defer tls.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		noteSignalDelivery()
	}()

	if got := _ccgo_write(tls, int32(pipe[1]), uintptr(unsafe.Pointer(&block[0])), uint64(len(block))); got != -1 {
		t.Fatalf("write returned %d, want -1", got)
	}
	if got := syscall.Errno(*(*int32)(unsafe.Pointer(libc.X__errno_location(tls)))); got != syscall.EINTR {
		t.Fatalf("errno = %v, want EINTR", got)
	}
}
