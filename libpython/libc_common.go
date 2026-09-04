// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package libpython

import (
	"syscall"
	"unsafe"

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
