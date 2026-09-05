// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	"modernc.org/libc/errno"
)

// Helpers shared by the per-OS libc supplements.

func setErrno(tls *libc.TLS, n int32) { *(*int32)(unsafe.Pointer(libc.X__errno_location(tls))) = n }

func errnoResult(tls *libc.TLS, err error) int32 {
	if err == nil {
		return 0
	}
	if e, ok := err.(syscall.Errno); ok {
		setErrno(tls, int32(e))
	} else {
		setErrno(tls, int32(errno.EIO))
	}
	return -1
}

func cBytes(p uintptr, n uint64) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
}

// Signals delivered through os/signal do not interrupt a Go goroutine blocked
// in a syscall. Blocking libc shims compare this generation around short poll
// intervals so CPython observes EINTR and can run its pending Python handler.
var (
	deliveredSignalGeneration atomic.Uint64
	observedSignalGeneration  atomic.Uint64
)

func noteSignalDelivery() { deliveredSignalGeneration.Add(1) }

func consumeSignalDelivery() bool {
	delivered := deliveredSignalGeneration.Load()
	for {
		observed := observedSignalGeneration.Load()
		if observed == delivered {
			return false
		}
		if observedSignalGeneration.CompareAndSwap(observed, delivered) {
			return true
		}
	}
}

// _ccgo_read preserves non-blocking reads, but makes blocking reads
// interruptible by signals delivered through signals_unix.go.
func _ccgo_read(tls *libc.TLS, fd int32, buf uintptr, count uint64) int64 {
	if count == 0 {
		n, err := unix.Read(int(fd), nil)
		if err != nil {
			return int64(errnoResult(tls, err))
		}
		return int64(n)
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return int64(errnoResult(tls, err))
	}
	if flags&unix.O_NONBLOCK != 0 {
		n, err := unix.Read(int(fd), cBytes(buf, count))
		if err != nil {
			return int64(errnoResult(tls, err))
		}
		return int64(n)
	}
	if consumeSignalDelivery() {
		setErrno(tls, int32(errno.EINTR))
		return -1
	}

	pollfd := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
	for {
		ready, err := unix.Poll(pollfd, 10)
		if err != nil {
			return int64(errnoResult(tls, err))
		}
		if ready != 0 {
			n, err := unix.Read(int(fd), cBytes(buf, count))
			if err != nil {
				return int64(errnoResult(tls, err))
			}
			return int64(n)
		}
		if consumeSignalDelivery() {
			setErrno(tls, int32(errno.EINTR))
			return -1
		}
	}
}

// _ccgo_write is the write-side counterpart of _ccgo_read.
func _ccgo_write(tls *libc.TLS, fd int32, buf uintptr, count uint64) int64 {
	data := cBytes(buf, count)
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return int64(errnoResult(tls, err))
	}
	if flags&unix.O_NONBLOCK != 0 {
		n, err := unix.Write(int(fd), data)
		if err != nil {
			return int64(errnoResult(tls, err))
		}
		return int64(n)
	}
	if consumeSignalDelivery() {
		setErrno(tls, int32(errno.EINTR))
		return -1
	}

	pollfd := []unix.PollFd{{Fd: fd, Events: unix.POLLOUT}}
	for {
		ready, err := unix.Poll(pollfd, 10)
		if err != nil {
			return int64(errnoResult(tls, err))
		}
		if ready != 0 {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
				return int64(errnoResult(tls, err))
			}
			n, err := unix.Write(int(fd), data)
			_, restoreErr := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
			if restoreErr != nil {
				return int64(errnoResult(tls, restoreErr))
			}
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				continue
			}
			if err != nil {
				return int64(errnoResult(tls, err))
			}
			return int64(n)
		}
		if consumeSignalDelivery() {
			setErrno(tls, int32(errno.EINTR))
			return -1
		}
	}
}
