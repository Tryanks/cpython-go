// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libpython

import (
	"reflect"
	"sync"
	"unsafe"

	"modernc.org/libc"
)

const (
	virtualCStackTop       = uintptr(1 << 30)
	virtualCStackSize      = uint64(8 << 20)
	virtualCStackFrameSize = uintptr(512)
)

var (
	tlsSPOffset uintptr
	tlsSPOnce   sync.Once
)

func tlsStackSlots(tls *libc.TLS) int {
	tlsSPOnce.Do(func() {
		field, ok := reflect.TypeOf(*tls).FieldByName("sp")
		if !ok || field.Type.Kind() != reflect.Int {
			panic("modernc.org/libc.TLS.sp is unavailable or has changed type")
		}
		tlsSPOffset = field.Offset
	})
	return *(*int)(unsafe.Pointer(uintptr(unsafe.Pointer(tls)) + tlsSPOffset))
}

// ccgo currently infers the undeclared macro replacement as C int. Keep the
// virtual range below INT_MAX; all generated callers widen this result to
// uintptr_t immediately.
func _ccgo_frame_address(tls *libc.TLS) int32 {
	return int32(virtualCStackTop - uintptr(tlsStackSlots(tls))*virtualCStackFrameSize)
}

func _pthread_get_stackaddr_np(tls *libc.TLS, thread uintptr) uintptr {
	return virtualCStackTop
}

func _pthread_get_stacksize_np(tls *libc.TLS, thread uintptr) uint64 {
	return virtualCStackSize
}
