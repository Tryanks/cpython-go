// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libpython

import (
	"unsafe"

	"modernc.org/libc"
)

// ccgo drops the bodies of these static-inline helpers from pycore_ceval.h.
// The CCGO-only depth field immediately precedes recursion_headroom. Using its
// relative offset lets existing, not-yet-regenerated target shards still
// compile; they do not contain calls to these helpers.
func ccgoCRecursionDepth(tstate uintptr) *int32 {
	offset := unsafe.Offsetof(TPyThreadState{}.Frecursion_headroom) - unsafe.Sizeof(int32(0))
	return (*int32)(unsafe.Pointer(tstate + offset))
}

func __Py_EnterRecursiveCallCCGO(tls *libc.TLS, tstate, where uintptr) int32 {
	depth := ccgoCRecursionDepth(tstate)
	*depth++
	if *depth <= 1000 {
		return 0
	}
	*depth--

	message := "maximum recursion depth exceeded"
	if where != 0 {
		message += libc.GoString(where)
	}
	p, err := libc.CString(message)
	if err != nil {
		XPyErr_NoMemory(tls)
		return 1
	}
	defer libc.Xfree(nil, p)
	XPyErr_SetString(tls, XPyExc_RecursionError, p)
	return 1
}

func __Py_LeaveRecursiveCallCCGO(_ *libc.TLS, tstate uintptr) {
	depth := ccgoCRecursionDepth(tstate)
	if *depth <= 0 {
		panic("unbalanced CCGO recursion guard")
	}
	*depth--
}
