// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package libpython

import (
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
)

func atomicLoadUint8(p uintptr) uint8 {
	word := p &^ 3
	shift := uint((p & 3) * 8)
	return uint8(atomic.LoadUint32((*uint32)(unsafe.Pointer(word))) >> shift)
}

func atomicCASUint8(p uintptr, old, new uint8) bool {
	word := p &^ 3
	shift := uint((p & 3) * 8)
	mask := uint32(0xff) << shift
	wp := (*uint32)(unsafe.Pointer(word))
	for {
		current := atomic.LoadUint32(wp)
		if uint8(current>>shift) != old {
			return false
		}
		replacement := current&^mask | uint32(new)<<shift
		if atomic.CompareAndSwapUint32(wp, current, replacement) {
			return true
		}
	}
}

func atomicStoreUint8(p uintptr, value uint8) {
	for {
		old := atomicLoadUint8(p)
		if atomicCASUint8(p, old, value) {
			return
		}
	}
}

func __Py_atomic_add_int32(tls *libc.TLS, p uintptr, value int32) int32 {
	return atomic.AddInt32((*int32)(unsafe.Pointer(p)), value) - value
}
func __Py_atomic_add_int64(tls *libc.TLS, p uintptr, value int64) int64 {
	return atomic.AddInt64((*int64)(unsafe.Pointer(p)), value) - value
}
func __Py_atomic_add_ssize(tls *libc.TLS, p uintptr, value int64) int64 {
	return atomic.AddInt64((*int64)(unsafe.Pointer(p)), value) - value
}
func __Py_atomic_add_uint64(tls *libc.TLS, p uintptr, value uint64) uint64 {
	return atomic.AddUint64((*uint64)(unsafe.Pointer(p)), value) - value
}
func __Py_atomic_add_uintptr(tls *libc.TLS, p uintptr, value uint64) uint64 {
	return atomic.AddUint64((*uint64)(unsafe.Pointer(p)), value) - value
}

func __Py_atomic_and_uintptr(tls *libc.TLS, p uintptr, value uint64) uint64 {
	q := (*uint64)(unsafe.Pointer(p))
	for {
		old := atomic.LoadUint64(q)
		if atomic.CompareAndSwapUint64(q, old, old&value) {
			return old
		}
	}
}

func __Py_atomic_or_uintptr(tls *libc.TLS, p uintptr, value uint64) uint64 {
	q := (*uint64)(unsafe.Pointer(p))
	for {
		old := atomic.LoadUint64(q)
		if atomic.CompareAndSwapUint64(q, old, old|value) {
			return old
		}
	}
}

func atomicCompareExchangeInt32(p, expected uintptr, desired int32) int32 {
	want := *(*int32)(unsafe.Pointer(expected))
	q := (*int32)(unsafe.Pointer(p))
	for {
		old := atomic.LoadInt32(q)
		if old != want {
			*(*int32)(unsafe.Pointer(expected)) = old
			return 0
		}
		if atomic.CompareAndSwapInt32(q, old, desired) {
			return 1
		}
	}
}

func atomicCompareExchangeUint32(p, expected uintptr, desired uint32) int32 {
	want := *(*uint32)(unsafe.Pointer(expected))
	q := (*uint32)(unsafe.Pointer(p))
	for {
		old := atomic.LoadUint32(q)
		if old != want {
			*(*uint32)(unsafe.Pointer(expected)) = old
			return 0
		}
		if atomic.CompareAndSwapUint32(q, old, desired) {
			return 1
		}
	}
}

func atomicCompareExchangeUint64(p, expected uintptr, desired uint64) int32 {
	want := *(*uint64)(unsafe.Pointer(expected))
	q := (*uint64)(unsafe.Pointer(p))
	for {
		old := atomic.LoadUint64(q)
		if old != want {
			*(*uint64)(unsafe.Pointer(expected)) = old
			return 0
		}
		if atomic.CompareAndSwapUint64(q, old, desired) {
			return 1
		}
	}
}

func __Py_atomic_compare_exchange_int(tls *libc.TLS, p, expected uintptr, desired int32) int32 {
	return atomicCompareExchangeInt32(p, expected, desired)
}
func __Py_atomic_compare_exchange_ptr(tls *libc.TLS, p, expected, desired uintptr) int32 {
	want := *(*uintptr)(unsafe.Pointer(expected))
	q := (*uintptr)(unsafe.Pointer(p))
	for {
		old := atomic.LoadUintptr(q)
		if old != want {
			*(*uintptr)(unsafe.Pointer(expected)) = old
			return 0
		}
		if atomic.CompareAndSwapUintptr(q, old, desired) {
			return 1
		}
	}
}
func __Py_atomic_compare_exchange_uint(tls *libc.TLS, p, expected uintptr, desired uint32) int32 {
	return atomicCompareExchangeUint32(p, expected, desired)
}
func __Py_atomic_compare_exchange_uint32(tls *libc.TLS, p, expected uintptr, desired uint32) int32 {
	return atomicCompareExchangeUint32(p, expected, desired)
}
func __Py_atomic_compare_exchange_uint64(tls *libc.TLS, p, expected uintptr, desired uint64) int32 {
	return atomicCompareExchangeUint64(p, expected, desired)
}
func __Py_atomic_compare_exchange_uint8(tls *libc.TLS, p, expected uintptr, desired uint8) int32 {
	want := *(*uint8)(unsafe.Pointer(expected))
	for {
		old := atomicLoadUint8(p)
		if old != want {
			*(*uint8)(unsafe.Pointer(expected)) = old
			return 0
		}
		if atomicCASUint8(p, old, desired) {
			return 1
		}
	}
}
func __Py_atomic_compare_exchange_uintptr(tls *libc.TLS, p, expected uintptr, desired uint64) int32 {
	return atomicCompareExchangeUint64(p, expected, desired)
}

func __Py_atomic_exchange_ptr(tls *libc.TLS, p, value uintptr) uintptr {
	return atomic.SwapUintptr((*uintptr)(unsafe.Pointer(p)), value)
}
func __Py_atomic_exchange_uint8(tls *libc.TLS, p uintptr, value uint8) uint8 {
	for {
		old := atomicLoadUint8(p)
		if atomicCASUint8(p, old, value) {
			return old
		}
	}
}
func __Py_atomic_exchange_uintptr(tls *libc.TLS, p uintptr, value uint64) uint64 {
	return atomic.SwapUint64((*uint64)(unsafe.Pointer(p)), value)
}

// Go's atomic operations are sequentially consistent, so explicit fences are
// unnecessary.
func __Py_atomic_fence_acquire(tls *libc.TLS) {}
func __Py_atomic_fence_release(tls *libc.TLS) {}
func __Py_atomic_fence_seq_cst(tls *libc.TLS) {}

func __Py_atomic_load_int(tls *libc.TLS, p uintptr) int32 {
	return atomic.LoadInt32((*int32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_int32_relaxed(tls *libc.TLS, p uintptr) int32 {
	return atomic.LoadInt32((*int32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_int64(tls *libc.TLS, p uintptr) int64 {
	return atomic.LoadInt64((*int64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_int64_relaxed(tls *libc.TLS, p uintptr) int64 {
	return atomic.LoadInt64((*int64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_llong_relaxed(tls *libc.TLS, p uintptr) int64 {
	return atomic.LoadInt64((*int64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_int_acquire(tls *libc.TLS, p uintptr) int32 {
	return atomic.LoadInt32((*int32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_int_relaxed(tls *libc.TLS, p uintptr) int32 {
	return atomic.LoadInt32((*int32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_ptr(tls *libc.TLS, p uintptr) uintptr {
	return atomic.LoadUintptr((*uintptr)(unsafe.Pointer(p)))
}
func __Py_atomic_load_ptr_acquire(tls *libc.TLS, p uintptr) uintptr {
	return atomic.LoadUintptr((*uintptr)(unsafe.Pointer(p)))
}
func __Py_atomic_load_ptr_relaxed(tls *libc.TLS, p uintptr) uintptr {
	return atomic.LoadUintptr((*uintptr)(unsafe.Pointer(p)))
}
func __Py_atomic_load_ssize(tls *libc.TLS, p uintptr) int64 {
	return atomic.LoadInt64((*int64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint16(tls *libc.TLS, p uintptr) uint16 { return _ccgo_AtomicLoadPUint16(p) }
func __Py_atomic_load_uint32(tls *libc.TLS, p uintptr) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint32_acquire(tls *libc.TLS, p uintptr) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint32_relaxed(tls *libc.TLS, p uintptr) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint64(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint64_acquire(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint64_relaxed(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uint8(tls *libc.TLS, p uintptr) uint8         { return atomicLoadUint8(p) }
func __Py_atomic_load_uint8_relaxed(tls *libc.TLS, p uintptr) uint8 { return atomicLoadUint8(p) }
func __Py_atomic_load_uint_relaxed(tls *libc.TLS, p uintptr) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uintptr(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_uintptr_relaxed(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}
func __Py_atomic_load_ullong_relaxed(tls *libc.TLS, p uintptr) uint64 {
	return atomic.LoadUint64((*uint64)(unsafe.Pointer(p)))
}

func __Py_atomic_store_int(tls *libc.TLS, p uintptr, value int32) {
	atomic.StoreInt32((*int32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_int64_relaxed(tls *libc.TLS, p uintptr, value int64) {
	atomic.StoreInt64((*int64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_llong_relaxed(tls *libc.TLS, p uintptr, value int64) {
	atomic.StoreInt64((*int64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_int_relaxed(tls *libc.TLS, p uintptr, value int32) {
	atomic.StoreInt32((*int32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_int_release(tls *libc.TLS, p uintptr, value int32) {
	atomic.StoreInt32((*int32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_ptr(tls *libc.TLS, p, value uintptr) {
	atomic.StoreUintptr((*uintptr)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_ptr_relaxed(tls *libc.TLS, p, value uintptr) {
	atomic.StoreUintptr((*uintptr)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_ptr_release(tls *libc.TLS, p, value uintptr) {
	atomic.StoreUintptr((*uintptr)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_ssize(tls *libc.TLS, p uintptr, value int64) {
	atomic.StoreInt64((*int64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint32(tls *libc.TLS, p uintptr, value uint32) {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint32_release(tls *libc.TLS, p uintptr, value uint32) {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint32_relaxed(tls *libc.TLS, p uintptr, value uint32) {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint64(tls *libc.TLS, p uintptr, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint64_relaxed(tls *libc.TLS, p uintptr, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint64_release(tls *libc.TLS, p uintptr, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_uint8(tls *libc.TLS, p uintptr, value uint8) { atomicStoreUint8(p, value) }
func __Py_atomic_store_uintptr(tls *libc.TLS, p uintptr, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), value)
}
func __Py_atomic_store_ullong_relaxed(tls *libc.TLS, p uintptr, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), value)
}
