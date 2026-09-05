//go:build windows

package libpython

import (
	"unsafe"

	"modernc.org/libc"
)

func writeWindowsCFormat(tls *libc.TLS, stream uintptr, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	if len(b) == 0 {
		return 0
	}
	p := libc.Xmalloc(tls, uint64(len(b)))
	if p == 0 {
		return -1
	}
	defer libc.Xfree(tls, p)
	copy(cBytes(p, uint64(len(b))), b)
	libc.Xfwrite(tls, p, 1, uint64(len(b)), stream)
	return int32(len(b))
}

func writeWindowsCFormatBuffer(dst uintptr, size uint64, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	if size != 0 && dst != 0 {
		n := uint64(len(b))
		if n >= size {
			n = size - 1
		}
		copy(cBytes(dst, n), b[:n])
		*(*byte)(unsafe.Pointer(dst + uintptr(n))) = 0
	}
	return int32(len(b))
}

func _ccgo_fprintf(tls *libc.TLS, stream, format, va uintptr) int32 {
	return writeWindowsCFormat(tls, stream, format, va)
}

func _ccgo_printf(tls *libc.TLS, format, va uintptr) int32 {
	return writeWindowsCFormat(tls, libc.X__acrt_iob_func(tls, 1), format, va)
}

func _ccgo_snprintf(tls *libc.TLS, dst uintptr, size uint64, format, va uintptr) int32 {
	return writeWindowsCFormatBuffer(dst, size, format, va)
}

func _ccgo___builtin_snprintf(tls *libc.TLS, dst uintptr, size uint64, format, va uintptr) int32 {
	return writeWindowsCFormatBuffer(dst, size, format, va)
}

func _ccgo___mingw_vsnprintf(tls *libc.TLS, dst uintptr, size uint64, format, va uintptr) int32 {
	return writeWindowsCFormatBuffer(dst, size, format, va)
}

func _ccgo_sprintf(tls *libc.TLS, dst, format, va uintptr) int32 {
	b := cFormat(libc.GoString(format), va)
	copy(cBytes(dst, uint64(len(b))), b)
	*(*byte)(unsafe.Pointer(dst + uintptr(len(b)))) = 0
	return int32(len(b))
}

func _ccgo_vfprintf(tls *libc.TLS, stream, format, va uintptr) int32 {
	return writeWindowsCFormat(tls, stream, format, va)
}

func _ccgo_vsnprintf(tls *libc.TLS, dst uintptr, size uint64, format, va uintptr) int32 {
	return writeWindowsCFormatBuffer(dst, size, format, va)
}

func _ccgo_vsprintf(tls *libc.TLS, dst, format, va uintptr) int32 {
	return _ccgo_sprintf(tls, dst, format, va)
}
