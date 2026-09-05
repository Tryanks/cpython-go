//go:build windows

package libpython

import (
	"math"
	"math/bits"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"modernc.org/libc"
	"modernc.org/libc/errno"
)

const (
	errorInvalidFunction = 1
	errorInvalidHandle   = 6
	errorNotEnoughMemory = 8
	errorInvalidParam    = 87
	errorCallNotImpl     = 120
)

var (
	dllKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dllAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	dllVersion  = windows.NewLazySystemDLL("version.dll")
	dllWS2      = windows.NewLazySystemDLL("ws2_32.dll")
	dllIPHlp    = windows.NewLazySystemDLL("iphlpapi.dll")
	dllRPCRT4   = windows.NewLazySystemDLL("rpcrt4.dll")
	dllPathCch  = windows.NewLazySystemDLL("pathcch.dll")
	dllBCrypt   = windows.NewLazySystemDLL("bcrypt.dll")
	dllWinMM    = windows.NewLazySystemDLL("winmm.dll")
	dllUCRT     = windows.NewLazySystemDLL("ucrtbase.dll")
	procCache   sync.Map
)

type winProcKey struct {
	dll  *windows.LazyDLL
	name string
}

func cachedProc(dll *windows.LazyDLL, name string) *windows.LazyProc {
	key := winProcKey{dll, name}
	if p, ok := procCache.Load(key); ok {
		return p.(*windows.LazyProc)
	}
	p := dll.NewProc(name)
	actual, _ := procCache.LoadOrStore(key, p)
	return actual.(*windows.LazyProc)
}

func setErrno(tls *libc.TLS, value int32) {
	*(*int32)(unsafe.Pointer(libc.X_errno(tls))) = value
}

func cBytes(p uintptr, n uint64) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
}

func winErrno(err error, fallback uint32) uint32 {
	if err == nil || err == windows.ERROR_SUCCESS {
		return fallback
	}
	if value, ok := err.(syscall.Errno); ok && value != 0 {
		return uint32(value)
	}
	return fallback
}

func setWinError(tls *libc.TLS, err error, fallback uint32) {
	value := winErrno(err, fallback)
	tls.SetLastError(value)
	// modernc.org/libc v1.75.7's XGetLastError reads this errno slot.
	setErrno(tls, int32(value))
}

func callProc(dll *windows.LazyDLL, name string, args ...uintptr) (uintptr, error) {
	r, _, err := cachedProc(dll, name).Call(args...)
	return r, err
}

func callUCRTWithErrno(tls *libc.TLS, name string, args ...uintptr) uintptr {
	errnoPointer, _ := callProc(dllUCRT, "_errno")
	if errnoPointer != 0 {
		*(*int32)(unsafe.Pointer(errnoPointer)) = 0
	}
	r, _ := callProc(dllUCRT, name, args...)
	if errnoPointer != 0 {
		setErrno(tls, *(*int32)(unsafe.Pointer(errnoPointer)))
	}
	return r
}

func boolProc(tls *libc.TLS, dll *windows.LazyDLL, name string, args ...uintptr) int32 {
	r, err := callProc(dll, name, args...)
	if r == 0 {
		setWinError(tls, err, errorInvalidFunction)
	}
	return int32(r)
}

func handleProc(tls *libc.TLS, dll *windows.LazyDLL, name string, invalid uintptr, args ...uintptr) uintptr {
	r, err := callProc(dll, name, args...)
	if r == invalid {
		setWinError(tls, err, errorInvalidHandle)
	}
	return r
}

func u16ptr(p uintptr) *uint16 {
	if p == 0 {
		return nil
	}
	return (*uint16)(unsafe.Pointer(p))
}

func byteptr(p uintptr) *byte {
	if p == 0 {
		return nil
	}
	return (*byte)(unsafe.Pointer(p))
}

func readWide(p uintptr) []uint16 {
	if p == 0 {
		return nil
	}
	var result []uint16
	for *(*uint16)(unsafe.Pointer(p)) != 0 {
		result = append(result, *(*uint16)(unsafe.Pointer(p)))
		p += 2
	}
	return result
}

func wideString(p uintptr) string { return utf16String(readWide(p)) }

func writeWide(dst uintptr, capacity uint64, value string) (uint64, bool) {
	encoded := utf16.Encode([]rune(value))
	if dst == 0 || uint64(len(encoded)+1) > capacity {
		return uint64(len(encoded)), false
	}
	out := unsafe.Slice((*uint16)(unsafe.Pointer(dst)), int(capacity))
	copy(out, encoded)
	out[len(encoded)] = 0
	return uint64(len(encoded)), true
}

func widePtrArray(p uintptr) []string {
	var result []string
	for p != 0 {
		value := *(*uintptr)(unsafe.Pointer(p))
		if value == 0 {
			return result
		}
		result = append(result, wideString(value))
		p += unsafe.Sizeof(uintptr(0))
	}
	return result
}

func tmZone(tmv *Ttm) (string, int) {
	date := time.Date(int(tmv.Ftm_year)+1900, time.Month(tmv.Ftm_mon)+1, int(tmv.Ftm_mday), int(tmv.Ftm_hour), int(tmv.Ftm_min), int(tmv.Ftm_sec), 0, time.Local)
	name, offset := date.Zone()
	return name, offset
}

func fillTM(dst uintptr, value time.Time) {
	tm := (*Ttm)(unsafe.Pointer(dst))
	tm.Ftm_sec = int32(value.Second())
	tm.Ftm_min = int32(value.Minute())
	tm.Ftm_hour = int32(value.Hour())
	tm.Ftm_mday = int32(value.Day())
	tm.Ftm_mon = int32(value.Month()) - 1
	tm.Ftm_year = int32(value.Year()) - 1900
	tm.Ftm_wday = int32(value.Weekday())
	tm.Ftm_yday = int32(value.YearDay()) - 1
	_, current := value.Zone()
	standard, observesDaylight := localZoneOffsets(value.Year(), value.Location())
	tm.Ftm_isdst = 0
	if observesDaylight && current != standard {
		tm.Ftm_isdst = 1
	}
}

func localZoneOffsets(year int, location *time.Location) (standard int, observesDaylight bool) {
	jan := time.Date(year, time.January, 1, 12, 0, 0, 0, location)
	jul := time.Date(year, time.July, 1, 12, 0, 0, 0, location)
	_, janOffset := jan.Zone()
	_, julOffset := jul.Zone()
	if janOffset == julOffset {
		return janOffset, false
	}
	// Contemporary DST advances clocks, so the smaller seconds-east value is
	// the standard offset in both hemispheres.
	if janOffset < julOffset {
		return janOffset, true
	}
	return julOffset, true
}

// modernc keeps its Windows descriptors in a private handle table. These two
// narrow links let the UCRT compatibility calls share that table instead of
// accidentally handing synthetic descriptors to ucrtbase.dll.
type moderncWindowsFile struct {
	fd     int32
	hadErr bool
	_      [3]byte
	token  uintptr
	handle windows.Handle
}

//go:linkname moderncFdToFile modernc.org/libc.fdToFile
func moderncFdToFile(fd int32) (*moderncWindowsFile, bool)

//go:linkname moderncWrapFdHandle modernc.org/libc.wrapFdHandle
func moderncWrapFdHandle(handle windows.Handle) (uintptr, int32)

func fdHandle(tls *libc.TLS, fd int32) (windows.Handle, bool) {
	f, ok := moderncFdToFile(fd)
	if !ok {
		setErrno(tls, int32(errno.EBADF))
		tls.SetLastError(errorInvalidHandle)
		return 0, false
	}
	return f.handle, true
}

func ___timezone(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "__timezone")
	return r
}

func ___daylight(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "__daylight")
	return r
}

func ___doserrno(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "__doserrno")
	return r
}

func ___sys_errlist(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "__sys_errlist")
	return r
}

func ___sys_nerr(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "__sys_nerr")
	return r
}

func __set_errno(tls *libc.TLS, value int32) int32 { setErrno(tls, value); return 0 }

func __aligned_free(tls *libc.TLS, p uintptr) { _, _ = callProc(dllUCRT, "_aligned_free", p) }

func __heapmin(tls *libc.TLS) int32 { return 0 }

func __wfopen(tls *libc.TLS, path, mode uintptr) uintptr {
	pathname, err := libc.CString(wideString(path))
	if err != nil {
		setErrno(tls, int32(errno.ENOMEM))
		return 0
	}
	defer libc.Xfree(tls, pathname)
	modeName, err := libc.CString(wideString(mode))
	if err != nil {
		setErrno(tls, int32(errno.ENOMEM))
		return 0
	}
	defer libc.Xfree(tls, modeName)
	return libc.Xfopen(tls, pathname, modeName)
}

func __get_osfhandle(tls *libc.TLS, fd int32) int64 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	return int64(handle)
}

func __open_osfhandle(tls *libc.TLS, raw int64, flags int32) int32 {
	if raw == -1 {
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	_, fd := moderncWrapFdHandle(windows.Handle(uintptr(raw)))
	return fd
}

func _dup(tls *libc.TLS, fd int32) int32 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	current := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(current, handle, current, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	_, result := moderncWrapFdHandle(duplicate)
	return result
}

func _clearerr(tls *libc.TLS, stream uintptr) {
	fd := libc.Xfileno(tls, stream)
	if f, ok := moderncFdToFile(fd); ok {
		f.hadErr = false
	}
}

func _feof(tls *libc.TLS, stream uintptr) int32 {
	// modernc's Windows FILE wrapper does not distinguish EOF from I/O error.
	return 0
}

func __chsize_s(tls *libc.TLS, fd int32, size int64) int32 {
	if size < 0 {
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return int32(errno.EBADF)
	}
	var old int64
	if err := setFilePointerRaw(handle, 0, uintptr(unsafe.Pointer(&old)), windows.FILE_CURRENT); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		setErrno(tls, int32(errno.EBADF))
		return int32(errno.EBADF)
	}
	if err := setFilePointerRaw(handle, size, 0, windows.FILE_BEGIN); err != nil {
		setWinError(tls, err, errorInvalidParam)
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	if err := windows.SetEndOfFile(handle); err != nil {
		setWinError(tls, err, errorInvalidParam)
		setErrno(tls, int32(errno.EACCES))
		return int32(errno.EACCES)
	}
	_ = setFilePointerRaw(handle, old, 0, windows.FILE_BEGIN)
	return 0
}

func __locking(tls *libc.TLS, fd, mode, count int32) int32 {
	if mode < 0 || mode > 4 || count < 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	var offset int64
	if err := setFilePointerRaw(handle, 0, uintptr(unsafe.Pointer(&offset)), windows.FILE_CURRENT); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	overlapped := windows.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(uint64(offset) >> 32)}
	if mode == 0 {
		if err := windows.UnlockFileEx(handle, 0, uint32(count), uint32(uint64(uint32(count))>>32), &overlapped); err != nil {
			setWinError(tls, err, errorInvalidParam)
			setErrno(tls, int32(errno.EACCES))
			return -1
		}
		return 0
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if mode == 2 || mode == 4 {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(handle, flags, 0, uint32(count), uint32(uint64(uint32(count))>>32), &overlapped); err != nil {
		setWinError(tls, err, errorInvalidParam)
		setErrno(tls, int32(errno.EACCES))
		return -1
	}
	return 0
}

func __wgetcwd(tls *libc.TLS, dst uintptr, size int32) uintptr {
	wd, err := os.Getwd()
	if err != nil {
		setErrno(tls, int32(errno.EIO))
		return 0
	}
	encoded := utf16.Encode([]rune(wd))
	need := len(encoded) + 1
	if dst == 0 {
		if size > 0 && int(size) < need {
			setErrno(tls, int32(errno.ERANGE))
			return 0
		}
		dst = libc.Xmalloc(tls, uint64(need*2))
		if dst == 0 {
			setErrno(tls, int32(errno.ENOMEM))
			return 0
		}
		size = int32(need)
	}
	if size <= 0 || int(size) < need {
		setErrno(tls, int32(errno.ERANGE))
		return 0
	}
	writeWide(dst, uint64(size), wd)
	return dst
}

func __wputenv_s(tls *libc.TLS, name, value uintptr) int32 {
	key := wideString(name)
	if name == 0 || value == 0 || key == "" || strings.ContainsRune(key, '=') {
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	if err := os.Setenv(key, wideString(value)); err != nil {
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	return 0
}

func __wcsdup(tls *libc.TLS, src uintptr) uintptr {
	if src == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	n := uint64(len(readWide(src)) + 1)
	dst := libc.Xmalloc(tls, n*2)
	if dst == 0 {
		setErrno(tls, int32(errno.ENOMEM))
		return 0
	}
	copy(cBytes(dst, n*2), cBytes(src, n*2))
	return dst
}

func _wcsnlen(tls *libc.TLS, src uintptr, limit uint64) uint64 {
	for i := uint64(0); i < limit; i++ {
		if *(*uint16)(unsafe.Pointer(src + uintptr(i)*2)) == 0 {
			return i
		}
	}
	return limit
}

func _wmemchr(tls *libc.TLS, src uintptr, value uint16, count uint64) uintptr {
	for i := uint64(0); i < count; i++ {
		p := src + uintptr(i)*2
		if *(*uint16)(unsafe.Pointer(p)) == value {
			return p
		}
	}
	return 0
}

func _wcsstr(tls *libc.TLS, haystack, needle uintptr) uintptr {
	n := readWide(needle)
	if len(n) == 0 {
		return haystack
	}
	h := readWide(haystack)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return haystack + uintptr(i)*2
		}
	}
	return 0
}

func _wcstok_s(tls *libc.TLS, src, delimiters, context uintptr) uintptr {
	if src == 0 {
		src = *(*uintptr)(unsafe.Pointer(context))
	}
	if src == 0 {
		return 0
	}
	isDelimiter := func(c uint16) bool {
		for p := delimiters; *(*uint16)(unsafe.Pointer(p)) != 0; p += 2 {
			if *(*uint16)(unsafe.Pointer(p)) == c {
				return true
			}
		}
		return false
	}
	for isDelimiter(*(*uint16)(unsafe.Pointer(src))) {
		src += 2
	}
	if *(*uint16)(unsafe.Pointer(src)) == 0 {
		*(*uintptr)(unsafe.Pointer(context)) = 0
		return 0
	}
	for p := src; ; p += 2 {
		c := *(*uint16)(unsafe.Pointer(p))
		if c == 0 {
			*(*uintptr)(unsafe.Pointer(context)) = 0
			return src
		}
		if isDelimiter(c) {
			*(*uint16)(unsafe.Pointer(p)) = 0
			*(*uintptr)(unsafe.Pointer(context)) = p + 2
			return src
		}
	}
}

func secureWideCopy(tls *libc.TLS, dst uintptr, capacity uint64, src []uint16) int32 {
	if dst == 0 || capacity == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	if uint64(len(src)+1) > capacity {
		*(*uint16)(unsafe.Pointer(dst)) = 0
		setErrno(tls, int32(errno.ERANGE))
		return int32(errno.ERANGE)
	}
	out := unsafe.Slice((*uint16)(unsafe.Pointer(dst)), int(capacity))
	copy(out, src)
	out[len(src)] = 0
	return 0
}

func _wcscpy_s(tls *libc.TLS, dst uintptr, capacity uint64, src uintptr) int32 {
	if src == 0 {
		if dst != 0 && capacity != 0 {
			*(*uint16)(unsafe.Pointer(dst)) = 0
		}
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	return secureWideCopy(tls, dst, capacity, readWide(src))
}

func _wcsncpy_s(tls *libc.TLS, dst uintptr, capacity uint64, src uintptr, count uint64) int32 {
	if src == 0 {
		if dst != 0 && capacity != 0 {
			*(*uint16)(unsafe.Pointer(dst)) = 0
		}
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	value := readWide(src)
	if count < uint64(len(value)) {
		value = value[:count]
	}
	return secureWideCopy(tls, dst, capacity, value)
}

func _wcscat_s(tls *libc.TLS, dst uintptr, capacity uint64, src uintptr) int32 {
	if dst == 0 || src == 0 || capacity == 0 {
		if dst != 0 && capacity != 0 {
			*(*uint16)(unsafe.Pointer(dst)) = 0
		}
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	used := _wcsnlen(tls, dst, capacity)
	if used == capacity {
		*(*uint16)(unsafe.Pointer(dst)) = 0
		setErrno(tls, int32(errno.EINVAL))
		return int32(errno.EINVAL)
	}
	value := readWide(src)
	if uint64(len(value)+1) > capacity-used {
		*(*uint16)(unsafe.Pointer(dst)) = 0
		setErrno(tls, int32(errno.ERANGE))
		return int32(errno.ERANGE)
	}
	return secureWideCopy(tls, dst+uintptr(used)*2, capacity-used, value)
}

func _wcscoll(tls *libc.TLS, a, b uintptr) int32 {
	r, _ := callProc(dllUCRT, "wcscoll", a, b)
	return int32(r)
}

func _wcsxfrm(tls *libc.TLS, dst, src uintptr, capacity uint64) uint64 {
	r := callUCRTWithErrno(tls, "wcsxfrm", dst, src, uintptr(capacity))
	return uint64(r)
}

func _wcstol(tls *libc.TLS, src, end uintptr, base int32) int32 {
	original := src
	for strings.ContainsRune(" \t\n\r\v\f", rune(*(*uint16)(unsafe.Pointer(src)))) {
		src += 2
	}
	negative := false
	if *(*uint16)(unsafe.Pointer(src)) == '-' {
		negative, src = true, src+2
	} else if *(*uint16)(unsafe.Pointer(src)) == '+' {
		src += 2
	}
	digit := func(c uint16) int32 {
		switch {
		case c >= '0' && c <= '9':
			return int32(c - '0')
		case c >= 'a' && c <= 'z':
			return int32(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			return int32(c-'A') + 10
		}
		return 99
	}
	if base != 0 && (base < 2 || base > 36) {
		setErrno(tls, int32(errno.EINVAL))
		if end != 0 {
			*(*uintptr)(unsafe.Pointer(end)) = original
		}
		return 0
	}
	if (base == 0 || base == 16) && *(*uint16)(unsafe.Pointer(src)) == '0' {
		x := *(*uint16)(unsafe.Pointer(src + 2))
		if (x == 'x' || x == 'X') && digit(*(*uint16)(unsafe.Pointer(src + 4))) < 16 {
			base, src = 16, src+4
		}
	}
	if base == 0 {
		if *(*uint16)(unsafe.Pointer(src)) == '0' {
			base = 8
		} else {
			base = 10
		}
	}
	start, value := src, uint64(0)
	limit := uint64(math.MaxInt32)
	if negative {
		limit++
	}
	overflow := false
	for d := digit(*(*uint16)(unsafe.Pointer(src))); d < base; d = digit(*(*uint16)(unsafe.Pointer(src))) {
		if value > (limit-uint64(d))/uint64(base) {
			overflow = true
		} else if !overflow {
			value = value*uint64(base) + uint64(d)
		}
		src += 2
	}
	if end != 0 {
		if src == start {
			*(*uintptr)(unsafe.Pointer(end)) = original
		} else {
			*(*uintptr)(unsafe.Pointer(end)) = src
		}
	}
	if overflow {
		setErrno(tls, int32(errno.ERANGE))
		if negative {
			return math.MinInt32
		}
		return math.MaxInt32
	}
	if negative {
		if value == uint64(math.MaxInt32)+1 {
			return math.MinInt32
		}
		return -int32(value)
	}
	if value > math.MaxInt32 {
		setErrno(tls, int32(errno.ERANGE))
		return math.MaxInt32
	}
	return int32(value)
}

func _mbrtowc(tls *libc.TLS, dst, src uintptr, n uint64, state uintptr) uint64 {
	r := callUCRTWithErrno(tls, "mbrtowc", dst, src, uintptr(n), state)
	return uint64(r)
}

func _iswctype(tls *libc.TLS, value, descriptor uint16) int32 {
	r, _ := callProc(dllUCRT, "_iswctype", uintptr(value), uintptr(descriptor))
	return int32(r)
}

func _towupper(tls *libc.TLS, value uint16) uint16 {
	r, _ := callProc(dllUCRT, "towupper", uintptr(value))
	return uint16(r)
}

func _wcsftime(tls *libc.TLS, dst uintptr, capacity uint64, format, tm uintptr) uint64 {
	if capacity == 0 {
		return 0
	}
	value := formatTM([]rune(wideString(format)), (*Ttm)(unsafe.Pointer(tm)))
	n, ok := writeWide(dst, capacity, value)
	if !ok {
		return 0
	}
	return n
}

func _localtime_s(tls *libc.TLS, dst, source uintptr) int32 {
	if dst == 0 || source == 0 {
		return int32(errno.EINVAL)
	}
	fillTM(dst, time.Unix(*(*int64)(unsafe.Pointer(source)), 0).In(time.Local))
	return 0
}

func _gmtime_s(tls *libc.TLS, dst, source uintptr) int32 {
	if dst == 0 || source == 0 {
		return int32(errno.EINVAL)
	}
	fillTM(dst, time.Unix(*(*int64)(unsafe.Pointer(source)), 0).UTC())
	return 0
}

func _clock(tls *libc.TLS) int32 {
	r, _ := callProc(dllUCRT, "clock")
	return int32(r)
}

func _erf(tls *libc.TLS, x float64) float64  { return math.Erf(x) }
func _erfc(tls *libc.TLS, x float64) float64 { return math.Erfc(x) }
func _exp2(tls *libc.TLS, x float64) float64 { return math.Exp2(x) }

func _strncat(tls *libc.TLS, dst, src uintptr, n uint64) uintptr {
	return _ccgo_strncat(tls, dst, src, n)
}

func _strnicmp(tls *libc.TLS, a, b uintptr, n uint64) int32 {
	r, _ := callProc(dllUCRT, "_strnicmp", a, b, uintptr(n))
	return int32(r)
}

func _swprintf(tls *libc.TLS, dst uintptr, capacity uint64, format, va uintptr) int32 {
	value := string(cFormat(wideString(format), va))
	if _, ok := writeWide(dst, capacity, value); !ok {
		setErrno(tls, int32(errno.ERANGE))
		return -1
	}
	return int32(len(utf16.Encode([]rune(value))))
}

func _localeconv(tls *libc.TLS) uintptr {
	r, _ := callProc(dllUCRT, "localeconv")
	return r
}

var (
	signalMu       sync.Mutex
	windowsSignals = map[int32]uintptr{}
)

func _signal(tls *libc.TLS, number int32, handler uintptr) uintptr {
	signalMu.Lock()
	defer signalMu.Unlock()
	previous := windowsSignals[number]
	windowsSignals[number] = handler
	return previous
}

func __getch(tls *libc.TLS) int32  { r, _ := callProc(dllUCRT, "_getch"); return int32(r) }
func __getche(tls *libc.TLS) int32 { r, _ := callProc(dllUCRT, "_getche"); return int32(r) }
func __getwch(tls *libc.TLS) uint16 {
	r, _ := callProc(dllUCRT, "_getwch")
	return uint16(r)
}
func __getwche(tls *libc.TLS) uint16 {
	r, _ := callProc(dllUCRT, "_getwche")
	return uint16(r)
}
func __kbhit(tls *libc.TLS) int32 { r, _ := callProc(dllUCRT, "_kbhit"); return int32(r) }
func __putch(tls *libc.TLS, c int32) int32 {
	r, _ := callProc(dllUCRT, "_putch", uintptr(c))
	return int32(r)
}
func __putwch(tls *libc.TLS, c uint16) uint16 {
	r, _ := callProc(dllUCRT, "_putwch", uintptr(c))
	return uint16(r)
}
func __ungetch(tls *libc.TLS, c int32) int32 {
	r, _ := callProc(dllUCRT, "_ungetch", uintptr(c))
	return int32(r)
}
func __ungetwch(tls *libc.TLS, c uint16) uint16 {
	r, _ := callProc(dllUCRT, "_ungetwch", uintptr(c))
	return uint16(r)
}

var (
	spawnMu      sync.Mutex
	spawned      = map[int64]*os.Process{}
	nextSpawnKey atomic.Int64
)

func spawnWide(tls *libc.TLS, mode int32, path, argv, envp uintptr) int64 {
	if mode < 0 || mode > 4 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	args := widePtrArray(argv)
	if len(args) == 0 {
		args = []string{wideString(path)}
	}
	command := exec.Command(wideString(path), args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if envp != 0 {
		command.Env = widePtrArray(envp)
	}
	if err := command.Start(); err != nil {
		setErrno(tls, int32(errno.ENOENT))
		return -1
	}
	if mode == 0 || mode == 2 {
		err := command.Wait()
		status := int64(0)
		if err == nil {
			status = 0
		} else if exit, ok := err.(*exec.ExitError); ok {
			status = int64(exit.ExitCode())
		} else {
			setErrno(tls, int32(errno.ECHILD))
			return -1
		}
		if mode == 2 {
			os.Exit(int(status))
		}
		return status
	}
	if mode == 4 {
		if err := command.Process.Release(); err != nil {
			setErrno(tls, int32(errno.ECHILD))
			return -1
		}
		return 0
	}
	key := nextSpawnKey.Add(1)
	spawnMu.Lock()
	spawned[key] = command.Process
	spawnMu.Unlock()
	return key
}

func __wspawnv(tls *libc.TLS, mode int32, path, argv uintptr) int64 {
	return spawnWide(tls, mode, path, argv, 0)
}
func __wspawnve(tls *libc.TLS, mode int32, path, argv, envp uintptr) int64 {
	return spawnWide(tls, mode, path, argv, envp)
}

func __cwait(tls *libc.TLS, status uintptr, process int64, action int32) int64 {
	spawnMu.Lock()
	p := spawned[process]
	delete(spawned, process)
	spawnMu.Unlock()
	if p == nil {
		setErrno(tls, int32(errno.ECHILD))
		return -1
	}
	state, err := p.Wait()
	if err != nil {
		setErrno(tls, int32(errno.ECHILD))
		return -1
	}
	if status != 0 {
		*(*int32)(unsafe.Pointer(status)) = int32(state.ExitCode())
	}
	return process
}

func __wexecv(tls *libc.TLS, path, argv uintptr) int64 {
	status := spawnWide(tls, 0, path, argv, 0)
	if status >= 0 {
		os.Exit(int(status))
	}
	return -1
}
func __wexecve(tls *libc.TLS, path, argv, envp uintptr) int64 {
	status := spawnWide(tls, 0, path, argv, envp)
	if status >= 0 {
		os.Exit(int(status))
	}
	return -1
}

func __wsystem(tls *libc.TLS, command uintptr) int32 {
	if command == 0 {
		return 1
	}
	err := exec.Command("cmd.exe", "/c", wideString(command)).Run()
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return int32(exit.ExitCode())
	}
	setErrno(tls, int32(errno.ENOENT))
	return -1
}

func __BitScanReverse64(tls *libc.TLS, index uintptr, mask uint64) uint8 {
	if mask == 0 {
		return 0
	}
	*(*uint32)(unsafe.Pointer(index)) = uint32(63 - bits.LeadingZeros64(mask))
	return 1
}

func _PyLong_AsSocket_t(tls *libc.TLS, obj uintptr) uint64 {
	return uint64(XPyLong_AsLongLong(tls, obj))
}

func _PyLong_FromSocket_t(tls *libc.TLS, value uint64) uintptr {
	return XPyLong_FromLongLong(tls, int64(value))
}

func PyBoolFromBool(tls *libc.TLS, value bool) uintptr {
	if value {
		return XPyBool_FromLong(tls, 1)
	}
	return XPyBool_FromLong(tls, 0)
}

func _ccgo_umask(tls *libc.TLS, mask int32) int32 {
	return int32(processUmask.Swap(uint32(mask) & 0777))
}

var processUmask atomic.Uint32

var _ccgo_in6addr_any Tin6_addr

func _ccgo_SetErrorMode(tls *libc.TLS, mode uint32) uint32 {
	r, _ := callProc(dllKernel32, "SetErrorMode", uintptr(mode))
	return uint32(r)
}

func _ccgo_GetShortPathNameW(tls *libc.TLS, path, buffer uintptr, capacity uint32) uint32 {
	r, err := callProc(dllKernel32, "GetShortPathNameW", path, buffer, uintptr(capacity))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func setFilePointerRaw(handle windows.Handle, distance int64, result uintptr, method uint32) error {
	r, err := callProc(dllKernel32, "SetFilePointerEx", uintptr(handle), uintptr(distance), result, uintptr(method))
	if r == 0 {
		return err
	}
	return nil
}

func _SetLastError(tls *libc.TLS, value uint32) {
	tls.SetLastError(value)
	setErrno(tls, int32(value))
	_, _ = callProc(dllKernel32, "SetLastError", uintptr(value))
}

func _VirtualAlloc(tls *libc.TLS, address uintptr, size uint64, allocationType, protect uint32) uintptr {
	result, err := windows.VirtualAlloc(address, uintptr(size), allocationType, protect)
	if result == 0 {
		setWinError(tls, err, errorNotEnoughMemory)
	}
	return result
}

func _VirtualFree(tls *libc.TLS, address uintptr, size uint64, freeType uint32) int32 {
	if err := windows.VirtualFree(address, uintptr(size), freeType); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _VirtualQuery(tls *libc.TLS, address, buffer uintptr, length uint64) uint64 {
	r, err := callProc(dllKernel32, "VirtualQuery", address, buffer, uintptr(length))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return uint64(r)
}

func _GetCurrentThreadStackLimits(tls *libc.TLS, low, high uintptr) {
	_, _ = callProc(dllKernel32, "GetCurrentThreadStackLimits", low, high)
}

func _SetThreadStackGuarantee(tls *libc.TLS, size uintptr) int32 {
	return boolProc(tls, dllKernel32, "SetThreadStackGuarantee", size)
}

func _InitializeSRWLock(tls *libc.TLS, lock uintptr) {
	_, _ = callProc(dllKernel32, "InitializeSRWLock", lock)
}

func _AcquireSRWLockExclusive(tls *libc.TLS, lock uintptr) {
	_, _ = callProc(dllKernel32, "AcquireSRWLockExclusive", lock)
}

func _ReleaseSRWLockExclusive(tls *libc.TLS, lock uintptr) {
	_, _ = callProc(dllKernel32, "ReleaseSRWLockExclusive", lock)
}

func _InitializeConditionVariable(tls *libc.TLS, condition uintptr) {
	_, _ = callProc(dllKernel32, "InitializeConditionVariable", condition)
}

func _SleepConditionVariableSRW(tls *libc.TLS, condition, lock uintptr, milliseconds, flags uint32) int32 {
	return boolProc(tls, dllKernel32, "SleepConditionVariableSRW", condition, lock, uintptr(milliseconds), uintptr(flags))
}

func _WakeConditionVariable(tls *libc.TLS, condition uintptr) {
	_, _ = callProc(dllKernel32, "WakeConditionVariable", condition)
}

func _WakeAllConditionVariable(tls *libc.TLS, condition uintptr) {
	_, _ = callProc(dllKernel32, "WakeAllConditionVariable", condition)
}

func _TlsAlloc(tls *libc.TLS) uint32 {
	r, err := callProc(dllKernel32, "TlsAlloc")
	if uint32(r) == 0xffffffff {
		setWinError(tls, err, errorNotEnoughMemory)
	}
	return uint32(r)
}

func _TlsFree(tls *libc.TLS, index uint32) int32 {
	return boolProc(tls, dllKernel32, "TlsFree", uintptr(index))
}

func _TlsGetValue(tls *libc.TLS, index uint32) uintptr {
	r, err := callProc(dllKernel32, "TlsGetValue", uintptr(index))
	if r == 0 && err != windows.ERROR_SUCCESS {
		setWinError(tls, err, errorInvalidParam)
	}
	return r
}

func _TlsSetValue(tls *libc.TLS, index uint32, value uintptr) int32 {
	return boolProc(tls, dllKernel32, "TlsSetValue", uintptr(index), value)
}

func _SwitchToThread(tls *libc.TLS) int32 {
	r, _ := callProc(dllKernel32, "SwitchToThread")
	return int32(r)
}

func _CreateSemaphoreA(tls *libc.TLS, security uintptr, initial, maximum int32, name uintptr) uintptr {
	return handleProc(tls, dllKernel32, "CreateSemaphoreA", 0, security, uintptr(initial), uintptr(maximum), name)
}

func _WaitForMultipleObjects(tls *libc.TLS, count uint32, handles uintptr, waitAll int32, milliseconds uint32) uint32 {
	if count == 0 {
		setWinError(tls, windows.ERROR_INVALID_PARAMETER, errorInvalidParam)
		return windows.WAIT_FAILED
	}
	values := unsafe.Slice((*windows.Handle)(unsafe.Pointer(handles)), int(count))
	result, err := windows.WaitForMultipleObjects(values, waitAll != 0, milliseconds)
	if result == windows.WAIT_FAILED {
		setWinError(tls, err, errorInvalidHandle)
	}
	return result
}

func _ReleaseSemaphore(tls *libc.TLS, semaphore uintptr, releaseCount int32, previous uintptr) int32 {
	return boolProc(tls, dllKernel32, "ReleaseSemaphore", semaphore, uintptr(releaseCount), previous)
}

func _GetSystemTimePreciseAsFileTime(tls *libc.TLS, result uintptr) {
	_, _ = callProc(dllKernel32, "GetSystemTimePreciseAsFileTime", result)
}

func _OpenThread(tls *libc.TLS, access uint32, inherit int32, threadID uint32) uintptr {
	return handleProc(tls, dllKernel32, "OpenThread", 0, uintptr(access), uintptr(inherit), uintptr(threadID))
}

func _GetConsoleOutputCP(tls *libc.TLS) uint32 {
	r, err := callProc(dllKernel32, "GetConsoleOutputCP")
	if r == 0 {
		setWinError(tls, err, errorInvalidHandle)
	}
	return uint32(r)
}

func _GetFileInformationByHandleEx(tls *libc.TLS, handle uintptr, class int32, buffer uintptr, length uint32) int32 {
	if err := windows.GetFileInformationByHandleEx(windows.Handle(handle), uint32(class), byteptr(buffer), length); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _SetFileInformationByHandle(tls *libc.TLS, handle uintptr, class int32, buffer uintptr, length uint32) int32 {
	if err := windows.SetFileInformationByHandle(windows.Handle(handle), uint32(class), byteptr(buffer), length); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _GetHandleInformation(tls *libc.TLS, handle, flags uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetHandleInformation", handle, flags)
}

func _OpenProcess(tls *libc.TLS, access uint32, inherit int32, processID uint32) uintptr {
	handle, err := windows.OpenProcess(access, inherit != 0, processID)
	if err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return uintptr(handle)
}

func _TerminateProcess(tls *libc.TLS, handle uintptr, exitCode uint32) int32 {
	if err := windows.TerminateProcess(windows.Handle(handle), exitCode); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _CreateToolhelp32Snapshot(tls *libc.TLS, flags, processID uint32) uintptr {
	return handleProc(tls, dllKernel32, "CreateToolhelp32Snapshot", ^uintptr(0), uintptr(flags), uintptr(processID))
}

func _Module32FirstW(tls *libc.TLS, snapshot, entry uintptr) int32 {
	return boolProc(tls, dllKernel32, "Module32FirstW", snapshot, entry)
}

func _Module32NextW(tls *libc.TLS, snapshot, entry uintptr) int32 {
	return boolProc(tls, dllKernel32, "Module32NextW", snapshot, entry)
}

func _ReadProcessMemory(tls *libc.TLS, process, base, buffer uintptr, size uint64, read uintptr) int32 {
	return boolProc(tls, dllKernel32, "ReadProcessMemory", process, base, buffer, uintptr(size), read)
}

func _OpenFileMappingW(tls *libc.TLS, access uint32, inherit int32, name uintptr) uintptr {
	return handleProc(tls, dllKernel32, "OpenFileMappingW", 0, uintptr(access), uintptr(inherit), name)
}

func _SetFilePointerEx(tls *libc.TLS, handle uintptr, distance TLARGE_INTEGER, result uintptr, method uint32) int32 {
	value := *(*int64)(unsafe.Pointer(&distance))
	if err := setFilePointerRaw(windows.Handle(handle), value, result, method); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _AddDllDirectory(tls *libc.TLS, path uintptr) uintptr {
	return handleProc(tls, dllKernel32, "AddDllDirectory", 0, path)
}

func _RemoveDllDirectory(tls *libc.TLS, cookie uintptr) int32 {
	return boolProc(tls, dllKernel32, "RemoveDllDirectory", cookie)
}

func _GetNumberOfConsoleInputEvents(tls *libc.TLS, input, count uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetNumberOfConsoleInputEvents", input, count)
}

func _GetStringTypeW(tls *libc.TLS, infoType uint32, src uintptr, length int32, charTypes uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetStringTypeW", uintptr(infoType), src, uintptr(length), charTypes)
}

func _GetThreadTimes(tls *libc.TLS, thread, creation, exit, kernel, user uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetThreadTimes", thread, creation, exit, kernel, user)
}

func _GetTimeZoneInformation(tls *libc.TLS, info uintptr) uint32 {
	r, err := callProc(dllKernel32, "GetTimeZoneInformation", info)
	if uint32(r) == 0xffffffff {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _CreateWaitableTimerExW(tls *libc.TLS, security, name uintptr, flags, access uint32) uintptr {
	return handleProc(tls, dllKernel32, "CreateWaitableTimerExW", 0, security, name, uintptr(flags), uintptr(access))
}

func _SetWaitableTimerEx(tls *libc.TLS, timer, dueTime uintptr, period int32, completion, arg, reasonContext uintptr, tolerance uint32) int32 {
	return boolProc(tls, dllKernel32, "SetWaitableTimerEx", timer, dueTime, uintptr(period), completion, arg, reasonContext, uintptr(tolerance))
}

func _GetLocaleInfoA(tls *libc.TLS, locale, lcType uint32, data uintptr, capacity int32) int32 {
	return boolProc(tls, dllKernel32, "GetLocaleInfoA", uintptr(locale), uintptr(lcType), data, uintptr(capacity))
}

func _ExpandEnvironmentStringsW(tls *libc.TLS, src, dst uintptr, capacity uint32) uint32 {
	r, err := callProc(dllKernel32, "ExpandEnvironmentStringsW", src, dst, uintptr(capacity))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _GetErrorMode(tls *libc.TLS) uint32 {
	r, _ := callProc(dllKernel32, "GetErrorMode")
	return uint32(r)
}

func _CancelIoEx(tls *libc.TLS, handle, overlapped uintptr) int32 {
	if err := windows.CancelIoEx(windows.Handle(handle), (*windows.Overlapped)(unsafe.Pointer(overlapped))); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _ConnectNamedPipe(tls *libc.TLS, pipe, overlapped uintptr) int32 {
	err := windows.ConnectNamedPipe(windows.Handle(pipe), (*windows.Overlapped)(unsafe.Pointer(overlapped)))
	if err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _CreateNamedPipeW(tls *libc.TLS, name uintptr, openMode, pipeMode, maxInstances, outSize, inSize, timeout uint32, security uintptr) uintptr {
	handle, err := windows.CreateNamedPipe(u16ptr(name), openMode, pipeMode, maxInstances, outSize, inSize, timeout, (*windows.SecurityAttributes)(unsafe.Pointer(security)))
	if err != nil {
		setWinError(tls, err, errorInvalidParam)
		return uintptr(windows.InvalidHandle)
	}
	return uintptr(handle)
}

func _CompareStringOrdinal(tls *libc.TLS, a uintptr, aLen int32, b uintptr, bLen int32, ignoreCase int32) int32 {
	r, err := callProc(dllKernel32, "CompareStringOrdinal", a, uintptr(aLen), b, uintptr(bLen), uintptr(ignoreCase))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return int32(r)
}

func _DeleteProcThreadAttributeList(tls *libc.TLS, list uintptr) {
	_, _ = callProc(dllKernel32, "DeleteProcThreadAttributeList", list)
}

func _InitializeProcThreadAttributeList(tls *libc.TLS, list uintptr, count, flags uint32, size uintptr) int32 {
	return boolProc(tls, dllKernel32, "InitializeProcThreadAttributeList", list, uintptr(count), uintptr(flags), size)
}

func _UpdateProcThreadAttribute(tls *libc.TLS, list uintptr, flags uint32, attribute uint64, value uintptr, size uint64, previous, returnSize uintptr) int32 {
	return boolProc(tls, dllKernel32, "UpdateProcThreadAttribute", list, uintptr(flags), uintptr(attribute), value, uintptr(size), previous, returnSize)
}

func _GetLongPathNameW(tls *libc.TLS, path, buffer uintptr, capacity uint32) uint32 {
	r, err := callProc(dllKernel32, "GetLongPathNameW", path, buffer, uintptr(capacity))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _GetVersion(tls *libc.TLS) uint32 {
	r, _ := callProc(dllKernel32, "GetVersion")
	return uint32(r)
}

func _OpenEventW(tls *libc.TLS, access uint32, inherit int32, name uintptr) uintptr {
	return handleProc(tls, dllKernel32, "OpenEventW", 0, uintptr(access), uintptr(inherit), name)
}

func _OpenMutexW(tls *libc.TLS, access uint32, inherit int32, name uintptr) uintptr {
	return handleProc(tls, dllKernel32, "OpenMutexW", 0, uintptr(access), uintptr(inherit), name)
}

func _LCMapStringEx(tls *libc.TLS, locale uintptr, flags uint32, src uintptr, srcLen int32, dst uintptr, dstLen int32, version, reserved uintptr, sortHandle int64) int32 {
	r, err := callProc(dllKernel32, "LCMapStringEx", locale, uintptr(flags), src, uintptr(srcLen), dst, uintptr(dstLen), version, reserved, uintptr(sortHandle))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return int32(r)
}

func _ReleaseMutex(tls *libc.TLS, mutex uintptr) int32 {
	return boolProc(tls, dllKernel32, "ReleaseMutex", mutex)
}

func _SetNamedPipeHandleState(tls *libc.TLS, pipe, mode, maxCollection, timeout uintptr) int32 {
	if err := windows.SetNamedPipeHandleState(windows.Handle(pipe), (*uint32)(unsafe.Pointer(mode)), (*uint32)(unsafe.Pointer(maxCollection)), (*uint32)(unsafe.Pointer(timeout))); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _WaitNamedPipeW(tls *libc.TLS, name uintptr, timeout uint32) int32 {
	return boolProc(tls, dllKernel32, "WaitNamedPipeW", name, uintptr(timeout))
}

func _GetTickCount64(tls *libc.TLS) uint64 {
	r, _ := callProc(dllKernel32, "GetTickCount64")
	return uint64(r)
}

func _ResumeThread(tls *libc.TLS, thread uintptr) uint32 {
	r, err := callProc(dllKernel32, "ResumeThread", thread)
	if uint32(r) == 0xffffffff {
		setWinError(tls, err, errorInvalidHandle)
	}
	return uint32(r)
}

func _NeedCurrentDirectoryForExePathW(tls *libc.TLS, executable uintptr) int32 {
	r, _ := callProc(dllKernel32, "NeedCurrentDirectoryForExePathW", executable)
	return int32(r)
}

func _CopyFile2(tls *libc.TLS, source, destination, parameters uintptr) int32 {
	r, _ := callProc(dllKernel32, "CopyFile2", source, destination, parameters)
	return int32(r)
}

func _GetFinalPathNameByHandleW(tls *libc.TLS, handle, path uintptr, capacity, flags uint32) uint32 {
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(handle), u16ptr(path), capacity, flags)
	if n == 0 {
		setWinError(tls, err, errorInvalidHandle)
	}
	return n
}

func _SetEnvironmentVariableW(tls *libc.TLS, name, value uintptr) int32 {
	if err := windows.SetEnvironmentVariable(u16ptr(name), u16ptr(value)); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _GetLogicalDriveStringsW(tls *libc.TLS, capacity uint32, buffer uintptr) uint32 {
	n, err := windows.GetLogicalDriveStrings(capacity, u16ptr(buffer))
	if n == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return n
}

func _FindFirstVolumeW(tls *libc.TLS, name uintptr, capacity uint32) uintptr {
	handle, err := windows.FindFirstVolume(u16ptr(name), capacity)
	if err != nil {
		setWinError(tls, err, errorInvalidParam)
		return uintptr(windows.InvalidHandle)
	}
	return uintptr(handle)
}

func _FindNextVolumeW(tls *libc.TLS, handle, name uintptr, capacity uint32) int32 {
	if err := windows.FindNextVolume(windows.Handle(handle), u16ptr(name), capacity); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _FindVolumeClose(tls *libc.TLS, handle uintptr) int32 {
	if err := windows.FindVolumeClose(windows.Handle(handle)); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _GetVolumePathNamesForVolumeNameW(tls *libc.TLS, volume, paths uintptr, capacity uint32, required uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetVolumePathNamesForVolumeNameW", volume, paths, uintptr(capacity), required)
}

func _GetVolumePathNameW(tls *libc.TLS, file, volumePath uintptr, capacity uint32) int32 {
	return boolProc(tls, dllKernel32, "GetVolumePathNameW", file, volumePath, uintptr(capacity))
}

func _GetDriveTypeW(tls *libc.TLS, root uintptr) uint32 {
	r, err := callProc(dllKernel32, "GetDriveTypeW", root)
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _MoveFileExW(tls *libc.TLS, from, to uintptr, flags uint32) int32 {
	if err := windows.MoveFileEx(u16ptr(from), u16ptr(to), flags); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _CreateSymbolicLinkW(tls *libc.TLS, link, target uintptr, flags uint32) uint8 {
	if err := windows.CreateSymbolicLink(u16ptr(link), u16ptr(target), flags); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _GetProcessTimes(tls *libc.TLS, process, creation, exit, kernel, user uintptr) int32 {
	err := windows.GetProcessTimes(windows.Handle(process), (*windows.Filetime)(unsafe.Pointer(creation)), (*windows.Filetime)(unsafe.Pointer(exit)), (*windows.Filetime)(unsafe.Pointer(kernel)), (*windows.Filetime)(unsafe.Pointer(user)))
	if err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _GetDiskFreeSpaceExW(tls *libc.TLS, directory, available, total, free uintptr) int32 {
	err := windows.GetDiskFreeSpaceEx(u16ptr(directory), (*uint64)(unsafe.Pointer(available)), (*uint64)(unsafe.Pointer(total)), (*uint64)(unsafe.Pointer(free)))
	if err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _GetActiveProcessorCount(tls *libc.TLS, group uint16) uint32 {
	r, err := callProc(dllKernel32, "GetActiveProcessorCount", uintptr(group))
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _GenerateConsoleCtrlEvent(tls *libc.TLS, event, processGroup uint32) int32 {
	return boolProc(tls, dllKernel32, "GenerateConsoleCtrlEvent", uintptr(event), uintptr(processGroup))
}

func _CreateIoCompletionPort(tls *libc.TLS, file, existing uintptr, key uint64, threads uint32) uintptr {
	return handleProc(tls, dllKernel32, "CreateIoCompletionPort", 0, file, existing, uintptr(key), uintptr(threads))
}

func _GetQueuedCompletionStatus(tls *libc.TLS, port, bytes, key, overlapped uintptr, milliseconds uint32) int32 {
	return boolProc(tls, dllKernel32, "GetQueuedCompletionStatus", port, bytes, key, overlapped, uintptr(milliseconds))
}

func _PostQueuedCompletionStatus(tls *libc.TLS, port uintptr, bytes uint32, key uint64, overlapped uintptr) int32 {
	return boolProc(tls, dllKernel32, "PostQueuedCompletionStatus", port, uintptr(bytes), uintptr(key), overlapped)
}

func _RegisterWaitForSingleObject(tls *libc.TLS, result, object, callback, context uintptr, milliseconds, flags uint32) int32 {
	// A transpiled Go function pointer is not a native Win32 callback.
	_SetLastError(tls, errorCallNotImpl)
	return 0
}

func _UnregisterWait(tls *libc.TLS, wait uintptr) int32 {
	return boolProc(tls, dllKernel32, "UnregisterWait", wait)
}

func _UnregisterWaitEx(tls *libc.TLS, wait, event uintptr) int32 {
	return boolProc(tls, dllKernel32, "UnregisterWaitEx", wait, event)
}

func _AddVectoredExceptionHandler(tls *libc.TLS, first uint32, handler uintptr) uintptr {
	// A transpiled Go function pointer is not a native vectored handler.
	_SetLastError(tls, errorCallNotImpl)
	return 0
}

func _RemoveVectoredExceptionHandler(tls *libc.TLS, handle uintptr) uint32 {
	_SetLastError(tls, errorCallNotImpl)
	return 0
}

func _PssCaptureSnapshot(tls *libc.TLS, process uintptr, flags, contextFlags uint32, snapshot uintptr) uint32 {
	r, _ := callProc(dllKernel32, "PssCaptureSnapshot", process, uintptr(flags), uintptr(contextFlags), snapshot)
	return uint32(r)
}

func _PssQuerySnapshot(tls *libc.TLS, snapshot uintptr, class int32, buffer uintptr, length uint32) uint32 {
	r, _ := callProc(dllKernel32, "PssQuerySnapshot", snapshot, uintptr(class), buffer, uintptr(length))
	return uint32(r)
}

func _PssFreeSnapshot(tls *libc.TLS, process, snapshot uintptr) uint32 {
	r, _ := callProc(dllKernel32, "PssFreeSnapshot", process, snapshot)
	return uint32(r)
}

func _RtlSecureZeroMemory(tls *libc.TLS, dst uintptr, size uint64) uintptr {
	clear(cBytes(dst, size))
	return dst
}

func _BCryptGenRandom(tls *libc.TLS, algorithm, buffer uintptr, length, flags uint32) int32 {
	r, _ := callProc(dllBCrypt, "BCryptGenRandom", algorithm, buffer, uintptr(length), uintptr(flags))
	return int32(r)
}

func _Beep(tls *libc.TLS, frequency, duration uint32) int32 {
	return boolProc(tls, dllKernel32, "Beep", uintptr(frequency), uintptr(duration))
}

func _GetNamedPipeHandleStateW(tls *libc.TLS, pipe, state, instances, maxCollection, timeout, user uintptr, userCapacity uint32) int32 {
	err := windows.GetNamedPipeHandleState(
		windows.Handle(pipe),
		(*uint32)(unsafe.Pointer(state)),
		(*uint32)(unsafe.Pointer(instances)),
		(*uint32)(unsafe.Pointer(maxCollection)),
		(*uint32)(unsafe.Pointer(timeout)),
		u16ptr(user),
		userCapacity,
	)
	if err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

func _PathCchCombineEx(tls *libc.TLS, output uintptr, capacity uint64, first, second uintptr, flags uint32) int32 {
	r, _ := callProc(dllPathCch, "PathCchCombineEx", output, uintptr(capacity), first, second, uintptr(flags))
	return int32(r)
}

func _PathCchSkipRoot(tls *libc.TLS, path, result uintptr) int32 {
	r, _ := callProc(dllPathCch, "PathCchSkipRoot", path, result)
	return int32(r)
}

func _PlaySoundW(tls *libc.TLS, sound, module uintptr, flags uint32) int32 {
	return boolProc(tls, dllWinMM, "PlaySoundW", sound, module, uintptr(flags))
}

func _ConvertStringSecurityDescriptorToSecurityDescriptorW(tls *libc.TLS, text uintptr, revision uint32, descriptor, size uintptr) int32 {
	return boolProc(tls, dllAdvapi32, "ConvertStringSecurityDescriptorToSecurityDescriptorW", text, uintptr(revision), descriptor, size)
}

func _LookupPrivilegeValueA(tls *libc.TLS, system, name, luid uintptr) int32 {
	return boolProc(tls, dllAdvapi32, "LookupPrivilegeValueA", system, name, luid)
}

func _AdjustTokenPrivileges(tls *libc.TLS, token uintptr, disable int32, newState uintptr, bufferLength uint32, previous, returnLength uintptr) int32 {
	r, err := callProc(
		dllAdvapi32,
		"AdjustTokenPrivileges",
		token,
		uintptr(disable),
		newState,
		uintptr(bufferLength),
		previous,
		returnLength,
	)
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	// A successful AdjustTokenPrivileges call reports
	// ERROR_NOT_ALL_ASSIGNED through GetLastError. Mirror that informational
	// status into the slot read by modernc's XGetLastError.
	value := winErrno(err, 0)
	tls.SetLastError(value)
	setErrno(tls, int32(value))
	return 1
}

func _RegCreateKeyW(tls *libc.TLS, key, subkey, result uintptr) int32 {
	r, _ := callProc(dllAdvapi32, "RegCreateKeyW", key, subkey, result)
	return int32(r)
}

func _RegDeleteKeyExW(tls *libc.TLS, key, subkey uintptr, access, reserved uint32) int32 {
	r, _ := callProc(dllAdvapi32, "RegDeleteKeyExW", key, subkey, uintptr(access), uintptr(reserved))
	return int32(r)
}

func _RegFlushKey(tls *libc.TLS, key uintptr) int32 {
	r, _ := callProc(dllAdvapi32, "RegFlushKey", key)
	return int32(r)
}

func _RegLoadKeyW(tls *libc.TLS, key, subkey, file uintptr) int32 {
	r, _ := callProc(dllAdvapi32, "RegLoadKeyW", key, subkey, file)
	return int32(r)
}

func _RegQueryInfoKeyW(tls *libc.TLS, key, class, classLength, reserved, subkeys, maxSubkeyLength, maxClassLength, values, maxValueNameLength, maxValueLength, securityDescriptor, lastWrite uintptr) int32 {
	r, _ := callProc(dllAdvapi32, "RegQueryInfoKeyW", key, class, classLength, reserved, subkeys, maxSubkeyLength, maxClassLength, values, maxValueNameLength, maxValueLength, securityDescriptor, lastWrite)
	return int32(r)
}

func _RegSaveKeyW(tls *libc.TLS, key, file, security uintptr) int32 {
	r, _ := callProc(dllAdvapi32, "RegSaveKeyW", key, file, security)
	return int32(r)
}

func _GetFileVersionInfoSizeW(tls *libc.TLS, filename, handle uintptr) uint32 {
	r, err := callProc(dllVersion, "GetFileVersionInfoSizeW", filename, handle)
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return uint32(r)
}

func _GetFileVersionInfoW(tls *libc.TLS, filename uintptr, handle, size uint32, buffer uintptr) int32 {
	if err := windows.GetFileVersionInfo(wideString(filename), handle, size, unsafe.Pointer(buffer)); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _VerQueryValueW(tls *libc.TLS, block, subBlock, result, length uintptr) int32 {
	if err := windows.VerQueryValue(unsafe.Pointer(block), wideString(subBlock), unsafe.Pointer(result), (*uint32)(unsafe.Pointer(length))); err != nil {
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
}

func _VerSetConditionMask(tls *libc.TLS, mask uint64, typeMask uint32, condition uint8) uint64 {
	r, _ := callProc(dllKernel32, "VerSetConditionMask", uintptr(mask), uintptr(typeMask), uintptr(condition))
	return uint64(r)
}

func _VerifyVersionInfoA(tls *libc.TLS, info uintptr, typeMask uint32, conditionMask uint64) int32 {
	return boolProc(tls, dllKernel32, "VerifyVersionInfoA", info, uintptr(typeMask), uintptr(conditionMask))
}

func _GetIfTable2Ex(tls *libc.TLS, level int32, table uintptr) uint32 {
	r, _ := callProc(dllIPHlp, "GetIfTable2Ex", uintptr(level), table)
	return uint32(r)
}

func _FreeMibTable(tls *libc.TLS, table uintptr) {
	_, _ = callProc(dllIPHlp, "FreeMibTable", table)
}

func _ConvertInterfaceLuidToNameW(tls *libc.TLS, luid, name uintptr, length uint64) uint32 {
	r, _ := callProc(dllIPHlp, "ConvertInterfaceLuidToNameW", luid, name, uintptr(length))
	return uint32(r)
}

func _if_nametoindex(tls *libc.TLS, name uintptr) uint32 {
	r, _ := callProc(dllIPHlp, "if_nametoindex", name)
	if r == 0 {
		setWSAError(tls)
	}
	return uint32(r)
}

func _if_indextoname(tls *libc.TLS, index uint32, name uintptr) uintptr {
	r, _ := callProc(dllIPHlp, "if_indextoname", uintptr(index), name)
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _UuidFromStringW(tls *libc.TLS, text, uuid uintptr) int32 {
	r, _ := callProc(dllRPCRT4, "UuidFromStringW", text, uuid)
	return int32(r)
}

func _UuidToStringW(tls *libc.TLS, uuid, text uintptr) int32 {
	r, _ := callProc(dllRPCRT4, "UuidToStringW", uuid, text)
	return int32(r)
}

func _RpcStringFreeW(tls *libc.TLS, text uintptr) int32 {
	r, _ := callProc(dllRPCRT4, "RpcStringFreeW", text)
	return int32(r)
}

func setWSAError(tls *libc.TLS) {
	r, _ := callProc(dllWS2, "WSAGetLastError")
	value := uint32(r)
	if value == 0 {
		value = errorInvalidFunction
	}
	tls.SetLastError(value)
	setErrno(tls, int32(value))
}

func _WSASetLastError(tls *libc.TLS, value int32) {
	_, _ = callProc(dllWS2, "WSASetLastError", uintptr(value))
	tls.SetLastError(uint32(value))
	setErrno(tls, value)
}

func _WSACleanup(tls *libc.TLS) int32 {
	if err := windows.WSACleanup(); err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSAConnect(tls *libc.TLS, socket uint64, address uintptr, addressLength int32, callerData, calleeData, sqos, gqos uintptr) int32 {
	r, _ := callProc(dllWS2, "WSAConnect", uintptr(socket), address, uintptr(addressLength), callerData, calleeData, sqos, gqos)
	if int32(r) == -1 {
		setWSAError(tls)
	}
	return int32(r)
}

func _WSADuplicateSocketW(tls *libc.TLS, socket uint64, processID uint32, info uintptr) int32 {
	err := windows.WSADuplicateSocket(windows.Handle(socket), processID, (*windows.WSAProtocolInfo)(unsafe.Pointer(info)))
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSAIoctl(tls *libc.TLS, socket uint64, code uint32, inBuffer uintptr, inLength uint32, outBuffer uintptr, outLength uint32, bytes, overlapped, completion uintptr) int32 {
	err := windows.WSAIoctl(
		windows.Handle(socket),
		code,
		byteptr(inBuffer),
		inLength,
		byteptr(outBuffer),
		outLength,
		(*uint32)(unsafe.Pointer(bytes)),
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		completion,
	)
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSARecv(tls *libc.TLS, socket uint64, buffers uintptr, count uint32, received, flags, overlapped, completion uintptr) int32 {
	err := windows.WSARecv(
		windows.Handle(socket),
		(*windows.WSABuf)(unsafe.Pointer(buffers)),
		count,
		(*uint32)(unsafe.Pointer(received)),
		(*uint32)(unsafe.Pointer(flags)),
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*byte)(unsafe.Pointer(completion)),
	)
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSARecvFrom(tls *libc.TLS, socket uint64, buffers uintptr, count uint32, received, flags, from, fromLength, overlapped, completion uintptr) int32 {
	err := windows.WSARecvFrom(
		windows.Handle(socket),
		(*windows.WSABuf)(unsafe.Pointer(buffers)),
		count,
		(*uint32)(unsafe.Pointer(received)),
		(*uint32)(unsafe.Pointer(flags)),
		(*windows.RawSockaddrAny)(unsafe.Pointer(from)),
		(*int32)(unsafe.Pointer(fromLength)),
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*byte)(unsafe.Pointer(completion)),
	)
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSASend(tls *libc.TLS, socket uint64, buffers uintptr, count uint32, sent uintptr, flags uint32, overlapped, completion uintptr) int32 {
	err := windows.WSASend(
		windows.Handle(socket),
		(*windows.WSABuf)(unsafe.Pointer(buffers)),
		count,
		(*uint32)(unsafe.Pointer(sent)),
		flags,
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*byte)(unsafe.Pointer(completion)),
	)
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSASendTo(tls *libc.TLS, socket uint64, buffers uintptr, count uint32, sent uintptr, flags uint32, to uintptr, toLength int32, overlapped, completion uintptr) int32 {
	err := windows.WSASendTo(
		windows.Handle(socket),
		(*windows.WSABuf)(unsafe.Pointer(buffers)),
		count,
		(*uint32)(unsafe.Pointer(sent)),
		flags,
		(*windows.RawSockaddrAny)(unsafe.Pointer(to)),
		toLength,
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*byte)(unsafe.Pointer(completion)),
	)
	if err != nil {
		setWSAError(tls)
		return -1
	}
	return 0
}

func _WSASocketW(tls *libc.TLS, family, socketType, protocol int32, info uintptr, group, flags uint32) uint64 {
	handle, err := windows.WSASocket(family, socketType, protocol, (*windows.WSAProtocolInfo)(unsafe.Pointer(info)), group, flags)
	if err != nil {
		setWSAError(tls)
		return ^uint64(0)
	}
	return uint64(handle)
}

func _WSAStringToAddressW(tls *libc.TLS, text uintptr, family int32, info, address, length uintptr) int32 {
	r, _ := callProc(dllWS2, "WSAStringToAddressW", text, uintptr(family), info, address, length)
	if int32(r) == -1 {
		setWSAError(tls)
	}
	return int32(r)
}

func ___WSAFDIsSet(tls *libc.TLS, socket uint64, set uintptr) int32 {
	if set == 0 {
		return 0
	}
	count := *(*uint32)(unsafe.Pointer(set))
	for i := uint32(0); i < count && i < 64; i++ {
		if *(*uint64)(unsafe.Pointer(set + 8 + uintptr(i)*8)) == socket {
			return 1
		}
	}
	return 0
}

func _recvfrom(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32, from, fromLength uintptr) int32 {
	r, _ := callProc(dllWS2, "recvfrom", uintptr(socket), buffer, uintptr(length), uintptr(flags), from, fromLength)
	if int32(r) == -1 {
		setWSAError(tls)
	}
	return int32(r)
}

func _sendto(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32, to uintptr, toLength int32) int32 {
	r, _ := callProc(dllWS2, "sendto", uintptr(socket), buffer, uintptr(length), uintptr(flags), to, uintptr(toLength))
	if int32(r) == -1 {
		setWSAError(tls)
	}
	return int32(r)
}

func _gethostbyaddr(tls *libc.TLS, address uintptr, length, family int32) uintptr {
	r, _ := callProc(dllWS2, "gethostbyaddr", address, uintptr(length), uintptr(family))
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _gethostbyname(tls *libc.TLS, name uintptr) uintptr {
	r, _ := callProc(dllWS2, "gethostbyname", name)
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _getservbyport(tls *libc.TLS, port int32, protocol uintptr) uintptr {
	r, _ := callProc(dllWS2, "getservbyport", uintptr(port), protocol)
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _getprotobyname(tls *libc.TLS, name uintptr) uintptr {
	r, _ := callProc(dllWS2, "getprotobyname", name)
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _inet_addr(tls *libc.TLS, address uintptr) uint32 {
	r, _ := callProc(dllWS2, "inet_addr", address)
	return uint32(r)
}

func _inet_pton(tls *libc.TLS, family int32, source, destination uintptr) int32 {
	r, _ := callProc(dllWS2, "inet_pton", uintptr(family), source, destination)
	if int32(r) == -1 {
		setWSAError(tls)
	}
	return int32(r)
}

func _inet_ntop(tls *libc.TLS, family int32, source, destination uintptr, size uint64) uintptr {
	r, _ := callProc(dllWS2, "inet_ntop", uintptr(family), source, destination, uintptr(size))
	if r == 0 {
		setWSAError(tls)
	}
	return r
}

func _ntohl(tls *libc.TLS, value uint32) uint32 { return bits.ReverseBytes32(value) }

func _getnameinfo(tls *libc.TLS, address uintptr, addressLength int32, host uintptr, hostLength uint32, service uintptr, serviceLength uint32, flags int32) int32 {
	r, _ := callProc(dllWS2, "getnameinfo", address, uintptr(addressLength), host, uintptr(hostLength), service, uintptr(serviceLength), uintptr(flags))
	return int32(r)
}

func _getaddrinfo(tls *libc.TLS, node, service, hints, result uintptr) int32 {
	if result == 0 {
		return 10022 // WSAEINVAL
	}
	*(*uintptr)(unsafe.Pointer(result)) = 0
	var nodeW, serviceW *uint16
	var err error
	if node != 0 {
		nodeW, err = syscall.UTF16PtrFromString(libc.GoString(node))
		if err != nil {
			return 11003 // WSATRY_AGAIN/EAI_FAIL
		}
	}
	if service != 0 {
		serviceW, err = syscall.UTF16PtrFromString(libc.GoString(service))
		if err != nil {
			return 11003
		}
	}
	var nativeHints *windows.AddrinfoW
	if hints != 0 {
		h := (*Taddrinfo)(unsafe.Pointer(hints))
		nativeHints = &windows.AddrinfoW{
			Flags:    h.Fai_flags,
			Family:   h.Fai_family,
			Socktype: h.Fai_socktype,
			Protocol: h.Fai_protocol,
		}
	}
	var native *windows.AddrinfoW
	if err := windows.GetAddrInfoW(nodeW, serviceW, nativeHints, &native); err != nil {
		if code, ok := err.(syscall.Errno); ok {
			return int32(code)
		}
		return 11003
	}
	defer windows.FreeAddrInfoW(native)
	var first, previous uintptr
	for current := native; current != nil; current = current.Next {
		entry := libc.Xmalloc(tls, uint64(unsafe.Sizeof(Taddrinfo{})))
		address := libc.Xmalloc(tls, uint64(current.Addrlen))
		if entry == 0 || address == 0 {
			if entry != 0 {
				libc.Xfree(tls, entry)
			}
			if address != 0 {
				libc.Xfree(tls, address)
			}
			_freeaddrinfo(tls, first)
			return 8 // WSA_NOT_ENOUGH_MEMORY / EAI_MEMORY
		}
		clear(cBytes(entry, uint64(unsafe.Sizeof(Taddrinfo{}))))
		copy(cBytes(address, uint64(current.Addrlen)), cBytes(current.Addr, uint64(current.Addrlen)))
		out := (*Taddrinfo)(unsafe.Pointer(entry))
		out.Fai_flags = current.Flags
		out.Fai_family = current.Family
		out.Fai_socktype = current.Socktype
		out.Fai_protocol = current.Protocol
		out.Fai_addrlen = uint64(current.Addrlen)
		out.Fai_addr = address
		if current.Canonname != nil {
			canon, allocErr := libc.CString(windows.UTF16PtrToString(current.Canonname))
			if allocErr != nil {
				libc.Xfree(tls, entry)
				libc.Xfree(tls, address)
				_freeaddrinfo(tls, first)
				return 8
			}
			out.Fai_canonname = canon
		}
		if first == 0 {
			first = entry
		} else {
			(*Taddrinfo)(unsafe.Pointer(previous)).Fai_next = entry
		}
		previous = entry
	}
	*(*uintptr)(unsafe.Pointer(result)) = first
	return 0
}

func _freeaddrinfo(tls *libc.TLS, current uintptr) {
	for current != 0 {
		entry := (*Taddrinfo)(unsafe.Pointer(current))
		next := entry.Fai_next
		if entry.Fai_canonname != 0 {
			libc.Xfree(tls, entry.Fai_canonname)
		}
		if entry.Fai_addr != 0 {
			libc.Xfree(tls, entry.Fai_addr)
		}
		libc.Xfree(tls, current)
		current = next
	}
}
