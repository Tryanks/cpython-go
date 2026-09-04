// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package libpython

import (
	"math"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
)

// Compiler builtins the transpiled code calls that modernc.org/libc does not
// provide. generator.go rewrites `iqlibc.X__builtin_<name>(` to
// `_ccgo_builtin_<name>(`.

func _ccgo_builtin_nextafter(tls *libc.TLS, x, y float64) float64 { return math.Nextafter(x, y) }
func _ccgo_builtin_asinh(tls *libc.TLS, x float64) float64        { return math.Asinh(x) }
func _ccgo_builtin_log1p(tls *libc.TLS, x float64) float64        { return math.Log1p(x) }
func _ccgo_builtin_fma(tls *libc.TLS, x, y, z float64) float64    { return math.FMA(x, y, z) }

// __builtin___strncat_chk(dst, src, n, dstsize): the _FORTIFY_SOURCE variant
// of strncat. ponytail: dstsize is ignored, same as plain strncat.
func _ccgo_builtin___strncat_chk(tls *libc.TLS, dst, src uintptr, n int32, dstsize uint64) uintptr {
	return libc.Xstrncat(tls, dst, src, uint64(n))
}

// Atomic loads missing from modernc.org/libc for 8/16 bit operands.

func _ccgo_AtomicLoadPUint8(addr uintptr) uint8 {
	return uint8(atomic.LoadUint32((*uint32)(unsafe.Pointer(addr&^3))) >> (8 * (addr & 3)))
}

func _ccgo_AtomicLoadPUint16(addr uintptr) uint16 {
	return uint16(atomic.LoadUint32((*uint32)(unsafe.Pointer(addr&^3))) >> (8 * (addr & 3)))
}

func _ccgo_AtomicStorePUint16(addr uintptr, v uint16) {
	p := (*uint32)(unsafe.Pointer(addr &^ 3))
	shift := 8 * (addr & 3)
	mask := uint32(0xffff) << shift
	for {
		old := atomic.LoadUint32(p)
		if atomic.CompareAndSwapUint32(p, old, old&^mask|uint32(v)<<shift) {
			return
		}
	}
}

func _ccgo_AtomicStorePUint8(addr uintptr, v uint8) {
	p := (*uint32)(unsafe.Pointer(addr &^ 3))
	shift := 8 * (addr & 3)
	mask := uint32(0xff) << shift
	for {
		old := atomic.LoadUint32(p)
		if atomic.CompareAndSwapUint32(p, old, old&^mask|uint32(v)<<shift) {
			return
		}
	}
}
