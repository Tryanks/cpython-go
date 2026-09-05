//go:build windows

package libpython

import (
	"bytes"
	"fmt"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
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
	errorProcNotFound    = 127
	socketError          = int32(-1)
	invalidSocket        = ^uint64(0)
)

var (
	dllKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dllAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	dllVersion  = windows.NewLazySystemDLL("version.dll")
	dllUser32   = windows.NewLazySystemDLL("user32.dll")
	dllWS2      = windows.NewLazySystemDLL("ws2_32.dll")
	dllIPHlp    = windows.NewLazySystemDLL("iphlpapi.dll")
	dllRPCRT4   = windows.NewLazySystemDLL("rpcrt4.dll")
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

func crtErrnoForWinError(value uint32) int32 {
	switch syscall.Errno(value) {
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
		return int32(errno.ENOENT)
	case windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION:
		return int32(errno.EACCES)
	case windows.ERROR_INVALID_HANDLE:
		return int32(errno.EBADF)
	case windows.ERROR_NOT_ENOUGH_MEMORY, windows.ERROR_OUTOFMEMORY:
		return int32(errno.ENOMEM)
	case windows.ERROR_FILE_EXISTS, windows.ERROR_ALREADY_EXISTS:
		return int32(errno.EEXIST)
	case windows.ERROR_BROKEN_PIPE:
		return int32(errno.EPIPE)
	case windows.ERROR_DISK_FULL:
		return int32(errno.ENOSPC)
	case windows.ERROR_DIR_NOT_EMPTY:
		return int32(errno.ENOTEMPTY)
	case windows.ERROR_FILENAME_EXCED_RANGE:
		return int32(errno.ENAMETOOLONG)
	case windows.ERROR_NO_DATA, windows.ERROR_INVALID_PARAMETER:
		return int32(errno.EINVAL)
	case windows.ERROR_DIRECTORY:
		return int32(errno.ENOTDIR)
	case windows.ERROR_OPERATION_ABORTED:
		return int32(errno.EINTR)
	default:
		return int32(errno.EINVAL)
	}
}

func setCRTWinError(tls *libc.TLS, err error, fallback uint32) {
	value := winErrno(err, fallback)
	tls.SetLastError(value)
	setErrno(tls, crtErrnoForWinError(value))
	if doserrno, _ := callProc(dllUCRT, "__doserrno"); doserrno != 0 {
		*(*uint32)(unsafe.Pointer(doserrno)) = value
	}
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

func callUCRTWithCRTErrors(tls *libc.TLS, name string, args ...uintptr) uintptr {
	errnoPointer, _ := callProc(dllUCRT, "_errno")
	doserrnoPointer, _ := callProc(dllUCRT, "__doserrno")
	if errnoPointer != 0 {
		*(*int32)(unsafe.Pointer(errnoPointer)) = 0
	}
	if doserrnoPointer != 0 {
		*(*uint32)(unsafe.Pointer(doserrnoPointer)) = 0
	}
	r, _ := callProc(dllUCRT, name, args...)
	if errnoPointer != 0 {
		setErrno(tls, *(*int32)(unsafe.Pointer(errnoPointer)))
	}
	if doserrnoPointer != 0 {
		tls.SetLastError(*(*uint32)(unsafe.Pointer(doserrnoPointer)))
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

var (
	windowsLocaleMu      sync.Mutex
	windowsLocaleNames   = [6]string{"C", "C", "C", "C", "C", "C"}
	windowsLocaleStrings = map[string]uintptr{}
)

func windowsStableCString(value string) uintptr {
	if p := windowsLocaleStrings[value]; p != 0 {
		return p
	}
	p, err := libc.CString(value)
	if err != nil {
		return 0
	}
	windowsLocaleStrings[value] = p
	return p
}

func windowsLocaleAllName(names [6]string) string {
	first := names[1]
	for i := 2; i < len(names); i++ {
		if names[i] != first {
			return "LC_COLLATE=" + names[1] +
				";LC_CTYPE=" + names[2] +
				";LC_MONETARY=" + names[3] +
				";LC_NUMERIC=" + names[4] +
				";LC_TIME=" + names[5]
		}
	}
	return first
}

func canonicalWindowsLocale(requested string) (string, bool) {
	upper := strings.ToUpper(requested)
	switch {
	case requested == "":
		return ".UTF-8", true
	case upper == "C" || upper == "POSIX":
		return "C", true
	case upper == "UTF-8" || upper == ".UTF-8" || strings.HasSuffix(upper, ".UTF-8"):
		return ".UTF-8", true
	case upper == "ENGLISH_UNITED STATES.1252":
		return "English_United States.1252", true
	}

	// UCRT accepts a code-page component up to 15 characters. CPython has a
	// regression test for that exact boundary and for its LC_ALL round trip.
	const prefix = "ENGLISH."
	if strings.HasPrefix(upper, prefix) {
		codePage := requested[len(prefix):]
		if len(codePage) == 15 && strings.TrimLeft(codePage, "0") == "1252" {
			return "English_United States.1252", true
		}
	}
	return "", false
}

func parseWindowsLocaleAll(requested string) ([6]string, bool) {
	names := windowsLocaleNames
	parts := strings.Split(requested, ";")
	prefixes := [...]string{"LC_COLLATE=", "LC_CTYPE=", "LC_MONETARY=", "LC_NUMERIC=", "LC_TIME="}
	if len(parts) != len(prefixes) {
		return names, false
	}
	for i, prefix := range prefixes {
		if !strings.HasPrefix(parts[i], prefix) {
			return names, false
		}
		name, ok := canonicalWindowsLocale(parts[i][len(prefix):])
		if !ok {
			return names, false
		}
		names[i+1] = name
	}
	return names, true
}

func recordWindowsLocale(category int32, name string) bool {
	if category != 0 {
		windowsLocaleNames[category] = name
		return true
	}
	if !strings.HasPrefix(name, "LC_") {
		for i := 1; i < len(windowsLocaleNames); i++ {
			windowsLocaleNames[i] = name
		}
		return true
	}

	parts := strings.Split(name, ";")
	prefixes := [...]string{"LC_COLLATE=", "LC_CTYPE=", "LC_MONETARY=", "LC_NUMERIC=", "LC_TIME="}
	if len(parts) != len(prefixes) {
		return false
	}
	for i, prefix := range prefixes {
		if !strings.HasPrefix(parts[i], prefix) || len(parts[i]) == len(prefix) {
			return false
		}
		windowsLocaleNames[i+1] = parts[i][len(prefix):]
	}
	return true
}

func setUCRTLocale(tls *libc.TLS, category int32, requested string) (string, bool) {
	value, err := libc.CString(requested)
	if err != nil {
		setErrno(tls, int32(errno.ENOMEM))
		return "", false
	}
	defer libc.Xfree(tls, value)
	result := callUCRTWithErrno(tls, "setlocale", uintptr(category), value)
	if result == 0 {
		return "", false
	}
	return libc.GoString(result), true
}

// modernc.org/libc's Windows setlocale always returns NULL. CPython needs a
// stable result before its allocator and codec registry exist, but obtains the
// actual filesystem and console encodings from GetACP/GetConsoleCP. Keep a
// deterministic C/UTF-8 locale model for the six UCRT categories.
func _ccgo_setlocale(tls *libc.TLS, category int32, locale uintptr) uintptr {
	if category < 0 || category >= int32(len(windowsLocaleNames)) {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}

	windowsLocaleMu.Lock()
	defer windowsLocaleMu.Unlock()

	if locale == 0 {
		if category != 0 {
			return windowsStableCString(windowsLocaleNames[category])
		}
		return windowsStableCString(windowsLocaleAllName(windowsLocaleNames))
	}

	requested := libc.GoString(locale)
	// UCRT remains the source of truth for aliases, code-page limits, and byte
	// character classification. Copy its mutable result into stable storage
	// and mirror it in the small model used by early-startup queries.
	if nativeName, ok := setUCRTLocale(tls, category, requested); ok {
		if !recordWindowsLocale(category, nativeName) {
			setErrno(tls, int32(errno.EINVAL))
			return 0
		}
		return windowsStableCString(nativeName)
	}

	if category == 0 && strings.HasPrefix(requested, "LC_") {
		names, ok := parseWindowsLocaleAll(requested)
		if !ok {
			setErrno(tls, int32(errno.EINVAL))
			return 0
		}
		windowsLocaleNames = names
		return windowsStableCString(windowsLocaleAllName(windowsLocaleNames))
	}

	name, ok := canonicalWindowsLocale(requested)
	if !ok {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	if _, ok := setUCRTLocale(tls, category, name); !ok {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}

	if category == 0 {
		for i := 1; i < len(windowsLocaleNames); i++ {
			windowsLocaleNames[i] = name
		}
	} else {
		windowsLocaleNames[category] = name
	}
	return windowsStableCString(name)
}

// The fixed UTF-8 locale above needs matching multibyte conversion. Windows
// wchar_t is UTF-16, so counts and limits are expressed in UTF-16 code units.
func _ccgo_mbstowcs(tls *libc.TLS, dst, src uintptr, limit uint64) uint64 {
	if src == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return ^uint64(0)
	}
	value := libc.GoString(src)
	if !utf8.ValidString(value) {
		setErrno(tls, int32(errno.EILSEQ))
		return ^uint64(0)
	}
	encoded := utf16.Encode([]rune(value))
	if dst == 0 {
		return uint64(len(encoded))
	}
	count := uint64(len(encoded))
	if count > limit {
		count = limit
	}
	if count != 0 {
		copy(unsafe.Slice((*uint16)(unsafe.Pointer(dst)), int(count)), encoded[:count])
	}
	if count < limit && count == uint64(len(encoded)) {
		*(*uint16)(unsafe.Pointer(dst + uintptr(count)*2)) = 0
	}
	return count
}

func _ccgo_wcstombs(tls *libc.TLS, dst, src uintptr, limit uint64) uint64 {
	if src == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return ^uint64(0)
	}
	units := readWide(src)
	runes := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		unit := units[i]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				setErrno(tls, int32(errno.EILSEQ))
				return ^uint64(0)
			}
			runes = append(runes, utf16.DecodeRune(rune(unit), rune(units[i+1])))
			i++
		case unit >= 0xdc00 && unit <= 0xdfff:
			setErrno(tls, int32(errno.EILSEQ))
			return ^uint64(0)
		default:
			runes = append(runes, rune(unit))
		}
	}
	if dst == 0 {
		return uint64(len(string(runes)))
	}
	var written uint64
	for _, r := range runes {
		var buffer [utf8.UTFMax]byte
		n := uint64(utf8.EncodeRune(buffer[:], r))
		if written+n > limit {
			return written
		}
		copy(cBytes(dst+uintptr(written), n), buffer[:n])
		written += n
	}
	if written < limit {
		*(*byte)(unsafe.Pointer(dst + uintptr(written))) = 0
	}
	return written
}

func _ccgo_wcschr(tls *libc.TLS, src uintptr, value uint16) uintptr {
	for {
		current := *(*uint16)(unsafe.Pointer(src))
		if current == value {
			return src
		}
		if current == 0 {
			return 0
		}
		src += 2
	}
}

func _ccgo_wcsncmp(tls *libc.TLS, left, right uintptr, count uint64) int32 {
	for index := uint64(0); index < count; index++ {
		offset := uintptr(index * 2)
		leftValue := *(*uint16)(unsafe.Pointer(left + offset))
		rightValue := *(*uint16)(unsafe.Pointer(right + offset))
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue == 0 {
			return 0
		}
	}
	return 0
}

// CPython's tokenizer uses a single-byte pushback after probing source
// encodings. modernc's Windows ungetc is an unconditional TODO; its FILE
// implementation is unbuffered, so rewinding by one byte provides the exact
// behavior needed by these readers.
func _ccgo_ungetc(tls *libc.TLS, c int32, stream uintptr) int32 {
	if c == -1 {
		return -1
	}
	if libc.Xfseek(tls, stream, -1, 1) != 0 {
		return -1
	}
	return int32(byte(c))
}

func _ccgo_OutputDebugStringW(tls *libc.TLS, text uintptr) {
	// Debugger output is advisory. Avoid modernc's TODO panic while keeping
	// fatal-error reporting on the process' real stderr intact.
}

// modernc returns EINVAL (a nonzero BOOL) when CloseHandle fails. Preserve the
// Win32 BOOL contract so callers can detect failure and read LastError.
func _ccgo_CloseHandle(tls *libc.TLS, handle uintptr) int32 {
	if err := windows.CloseHandle(windows.Handle(handle)); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

// modernc.org/libc stores failures from most Win32 wrappers in errno, while
// its GetLastError reads that same errno slot. CPython deliberately clears
// errno in several paths before consulting LastError. Prefer modernc's slot
// while an unrouted wrapper has populated it, then fall back to the independent
// LastError channel maintained by the shims below.
func _ccgo_GetLastError(tls *libc.TLS) uint32 {
	if value := *(*int32)(unsafe.Pointer(libc.X_errno(tls))); value != 0 {
		return uint32(value)
	}
	return tls.GetLastError()
}

func _ccgo_CreateDirectoryW(tls *libc.TLS, path, securityAttributes uintptr) int32 {
	return boolProc(tls, dllKernel32, "CreateDirectoryW", path, securityAttributes)
}

func _ccgo_CreateFileW(tls *libc.TLS, path uintptr, desiredAccess, shareMode uint32, securityAttributes uintptr, creationDisposition, flagsAndAttributes uint32, templateFile uintptr) uintptr {
	return handleProc(
		tls,
		dllKernel32,
		"CreateFileW",
		^uintptr(0),
		path,
		uintptr(desiredAccess),
		uintptr(shareMode),
		securityAttributes,
		uintptr(creationDisposition),
		uintptr(flagsAndAttributes),
		templateFile,
	)
}

func _ccgo_DeleteFileW(tls *libc.TLS, path uintptr) int32 {
	return boolProc(tls, dllKernel32, "DeleteFileW", path)
}

func _ccgo_FindClose(tls *libc.TLS, handle uintptr) int32 {
	return boolProc(tls, dllKernel32, "FindClose", handle)
}

func _ccgo_FindFirstFileW(tls *libc.TLS, path, data uintptr) uintptr {
	return handleProc(tls, dllKernel32, "FindFirstFileW", ^uintptr(0), path, data)
}

func _ccgo_FindNextFileW(tls *libc.TLS, handle, data uintptr) int32 {
	return boolProc(tls, dllKernel32, "FindNextFileW", handle, data)
}

func _ccgo_GetFileAttributesExW(tls *libc.TLS, path uintptr, infoLevel int32, information uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetFileAttributesExW", path, uintptr(infoLevel), information)
}

func _ccgo_GetFileAttributesW(tls *libc.TLS, path uintptr) uint32 {
	r, err := callProc(dllKernel32, "GetFileAttributesW", path)
	if uint32(r) == ^uint32(0) {
		setWinError(tls, err, errorInvalidFunction)
	}
	return uint32(r)
}

func _ccgo_GetFileInformationByHandle(tls *libc.TLS, handle, information uintptr) int32 {
	return boolProc(tls, dllKernel32, "GetFileInformationByHandle", handle, information)
}

func _ccgo_GetFileType(tls *libc.TLS, handle uintptr) uint32 {
	// FILE_TYPE_UNKNOWN (zero) may be a valid result, so clear LastError first
	// and only publish the native error when the call leaves one behind.
	_SetLastError(tls, 0)
	r, err := callProc(dllKernel32, "GetFileType", handle)
	if r == 0 && winErrno(err, 0) != 0 {
		setWinError(tls, err, errorInvalidHandle)
	}
	return uint32(r)
}

func _ccgo_MultiByteToWideChar(tls *libc.TLS, codePage, flags uint32, source uintptr, sourceLength int32, destination uintptr, destinationLength int32) int32 {
	r, err := callProc(
		dllKernel32,
		"MultiByteToWideChar",
		uintptr(codePage),
		uintptr(flags),
		source,
		uintptr(sourceLength),
		destination,
		uintptr(destinationLength),
	)
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return int32(r)
}

func _ccgo_RemoveDirectoryW(tls *libc.TLS, path uintptr) int32 {
	return boolProc(tls, dllKernel32, "RemoveDirectoryW", path)
}

func _ccgo_SetFileAttributesW(tls *libc.TLS, path uintptr, attributes uint32) int32 {
	return boolProc(tls, dllKernel32, "SetFileAttributesW", path, uintptr(attributes))
}

func _ccgo_WideCharToMultiByte(tls *libc.TLS, codePage, flags uint32, source uintptr, sourceLength int32, destination uintptr, destinationLength int32, defaultChar, usedDefaultChar uintptr) int32 {
	r, err := callProc(
		dllKernel32,
		"WideCharToMultiByte",
		uintptr(codePage),
		uintptr(flags),
		source,
		uintptr(sourceLength),
		destination,
		uintptr(destinationLength),
		defaultChar,
		usedDefaultChar,
	)
	if r == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return int32(r)
}

// LoadLibraryW and FreeLibrary are needed by CPython's optional Windows API
// probes. Native procedure addresses cannot be invoked through ccgo's Go
// function-pointer representation, so GetProcAddress reports an unavailable
// optional API instead of handing generated code an address that would crash
// when called. Built-in modules use their statically linked init functions.
func _ccgo_LoadLibraryW(tls *libc.TLS, filename uintptr) uintptr {
	if filename == 0 {
		setWinError(tls, windows.ERROR_INVALID_PARAMETER, errorInvalidParam)
		return 0
	}
	r, err := callProc(dllKernel32, "LoadLibraryW", filename)
	if r == 0 {
		setWinError(tls, err, errorInvalidFunction)
	}
	return r
}

func _ccgo_GetProcAddress(tls *libc.TLS, module, name uintptr) uintptr {
	// A FARPROC is a native machine-code address, whereas ccgo-translated C
	// calls require a Go function value registered through __ccgo_fp.
	setWinError(tls, windows.ERROR_PROC_NOT_FOUND, errorProcNotFound)
	return 0
}

func _ccgo_FreeLibrary(tls *libc.TLS, module uintptr) int32 {
	return boolProc(tls, dllKernel32, "FreeLibrary", module)
}

// Windows C long is 32-bit even on amd64 and arm64. modernc's generic
// non-Linux implementation uses LeadingZeros64 for its uint32 ulong alias,
// making every nonzero result 32 too large.
func _ccgo___builtin_clzl(tls *libc.TLS, value uint32) int32 {
	return int32(bits.LeadingZeros32(value))
}

var (
	windowsWideStringMu    sync.Mutex
	windowsWideStringCache = map[string]uintptr{}
	windowsEnvironmentMu   sync.Mutex
	windowsEnvironmentKey  string
	windowsEnvironmentList []uintptr
	windowsEnvironment     uintptr
)

func windowsStableWideString(tls *libc.TLS, value string) uintptr {
	windowsWideStringMu.Lock()
	defer windowsWideStringMu.Unlock()
	if p := windowsWideStringCache[value]; p != 0 {
		return p
	}
	encoded := utf16.Encode([]rune(value))
	p := libc.Xmalloc(tls, uint64((len(encoded)+1)*2))
	if p == 0 {
		setErrno(tls, int32(errno.ENOMEM))
		return 0
	}
	if len(encoded) != 0 {
		copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(encoded)), encoded)
	}
	*(*uint16)(unsafe.Pointer(p + uintptr(len(encoded))*2)) = 0
	windowsWideStringCache[value] = p
	return p
}

// modernc's cached wide environment drops its final entry and does not track
// Go-side Setenv calls. Use the process environment and return stable CRT-like
// storage instead.
func _ccgo__wgetenv(tls *libc.TLS, name uintptr) uintptr {
	if name == 0 {
		return 0
	}
	value, ok := os.LookupEnv(wideString(name))
	if !ok {
		return 0
	}
	return windowsStableWideString(tls, value)
}

// modernc's bootWinEnviron omits the required trailing null pointer. Reading
// its _wenviron can therefore walk beyond the Go slice, which is especially
// visible on arm64 when the next backing-array slot is nonzero. Publish an
// explicitly terminated, process-environment-backed array instead.
func _ccgo___p__wenviron(tls *libc.TLS) uintptr {
	entries := os.Environ()
	// GetEnvironmentStringsW includes hidden drive-current-directory entries
	// such as "=C:=...". UCRT's _wenviron omits them; exposing them makes
	// posixmodule create an empty os.environ key that cannot later be removed.
	filtered := entries[:0]
	for _, entry := range entries {
		if !strings.HasPrefix(entry, "=") {
			filtered = append(filtered, entry)
		}
	}
	entries = filtered
	key := strings.Join(entries, "\x00")

	windowsEnvironmentMu.Lock()
	defer windowsEnvironmentMu.Unlock()
	if windowsEnvironment == 0 || key != windowsEnvironmentKey {
		list := make([]uintptr, 0, len(entries)+1)
		for _, entry := range entries {
			list = append(list, windowsStableWideString(tls, entry))
		}
		list = append(list, 0)
		windowsEnvironmentList = list
		windowsEnvironment = uintptr(unsafe.Pointer(&windowsEnvironmentList[0]))
		windowsEnvironmentKey = key
	}
	return uintptr(unsafe.Pointer(&windowsEnvironment))
}

func _ccgo__wputenv(tls *libc.TLS, environment uintptr) int32 {
	if environment == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	text := wideString(environment)
	separator := strings.IndexByte(text, '=')
	if separator == 0 && len(text) > 1 {
		// Windows reserves leading '=' names for drive-current-directory
		// entries; their assignment delimiter is the next '='.
		if rest := strings.IndexByte(text[1:], '='); rest >= 0 {
			separator = rest + 1
		}
	}
	if separator <= 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	name, value := text[:separator], text[separator+1:]
	if strings.HasPrefix(name, "=") &&
		(len(name) != 3 || name[2] != ':' || !((name[1] >= 'A' && name[1] <= 'Z') || (name[1] >= 'a' && name[1] <= 'z'))) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	var err error
	if value == "" {
		err = os.Unsetenv(name)
	} else {
		err = os.Setenv(name, value)
	}
	if err != nil {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return 0
}

type windowsDescriptorMode struct {
	text bool
	eof  bool
}

var (
	windowsDescriptorModeMu sync.Mutex
	windowsDescriptorModes  = map[int32]windowsDescriptorMode{}
)

func setWindowsDescriptorMode(fd int32, text bool) {
	windowsDescriptorModeMu.Lock()
	windowsDescriptorModes[fd] = windowsDescriptorMode{text: text}
	windowsDescriptorModeMu.Unlock()
}

func removeWindowsDescriptorMode(fd int32) {
	windowsDescriptorModeMu.Lock()
	delete(windowsDescriptorModes, fd)
	windowsDescriptorModeMu.Unlock()
}

func windowsOpenTextMode(flags int32) bool {
	const ucrtOBinary = int32(0x8000)
	return flags&ucrtOBinary == 0
}

func copyWindowsDescriptorMode(from, to int32) {
	windowsDescriptorModeMu.Lock()
	windowsDescriptorModes[to] = windowsDescriptorModes[from]
	windowsDescriptorModeMu.Unlock()
}

func windowsDescriptorModeFor(fd int32) windowsDescriptorMode {
	windowsDescriptorModeMu.Lock()
	defer windowsDescriptorModeMu.Unlock()
	return windowsDescriptorModes[fd]
}

func setWindowsDescriptorEOF(fd int32, value bool) {
	windowsDescriptorModeMu.Lock()
	mode := windowsDescriptorModes[fd]
	mode.eof = value
	windowsDescriptorModes[fd] = mode
	windowsDescriptorModeMu.Unlock()
}

// modernc's _wopen passes UTF-16 storage to a narrow GoString helper, losing
// both ordinary wide paths and unpaired surrogate code units. Call CreateFileW
// with the original UTF-16 pointer and install its handle in modernc's table.
func _ccgo__wopen(tls *libc.TLS, pathname uintptr, flags int32, args uintptr) int32 {
	if pathname == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}

	const (
		ucrtOAppend     = 0x0008
		ucrtORandom     = 0x0010
		ucrtOSequential = 0x0020
		ucrtOTemporary  = 0x0040
		ucrtONoinherit  = 0x0080
		ucrtOCreat      = 0x0100
		ucrtOTrunc      = 0x0200
		ucrtOExcl       = 0x0400
		ucrtOShortLived = 0x1000
	)

	var desiredAccess uint32
	switch flags & 0x3 {
	case 0:
		desiredAccess = windows.GENERIC_READ
	case 1:
		desiredAccess = windows.GENERIC_WRITE
	case 2:
		desiredAccess = windows.GENERIC_READ | windows.GENERIC_WRITE
	default:
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	if flags&ucrtOCreat != 0 {
		desiredAccess |= windows.GENERIC_WRITE
	}
	if flags&ucrtOAppend != 0 {
		desiredAccess &^= windows.GENERIC_WRITE
		desiredAccess |= windows.FILE_APPEND_DATA
	}

	shareMode := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if flags&ucrtORandom != 0 {
		attributes |= windows.FILE_FLAG_RANDOM_ACCESS
	}
	if flags&ucrtOSequential != 0 {
		attributes |= windows.FILE_FLAG_SEQUENTIAL_SCAN
	}
	if flags&(ucrtOTemporary|ucrtOShortLived) != 0 {
		attributes |= windows.FILE_ATTRIBUTE_TEMPORARY
	}
	if flags&ucrtOTemporary != 0 {
		attributes |= windows.FILE_FLAG_DELETE_ON_CLOSE
		shareMode |= windows.FILE_SHARE_DELETE
	}
	if flags&ucrtOCreat != 0 {
		mode := uint32(0x180) // _S_IREAD | _S_IWRITE
		if args != 0 {
			mode = libc.VaUint32(&args)
		}
		if mode&0x80 == 0 { // _S_IWRITE
			attributes |= windows.FILE_ATTRIBUTE_READONLY
		}
	}

	creationDisposition := uint32(windows.OPEN_EXISTING)
	switch {
	case flags&(ucrtOCreat|ucrtOExcl) == ucrtOCreat|ucrtOExcl:
		creationDisposition = windows.CREATE_NEW
	case flags&(ucrtOCreat|ucrtOTrunc) == ucrtOCreat|ucrtOTrunc:
		creationDisposition = windows.CREATE_ALWAYS
	case flags&ucrtOCreat != 0:
		creationDisposition = windows.OPEN_ALWAYS
	case flags&ucrtOTrunc != 0:
		creationDisposition = windows.TRUNCATE_EXISTING
	}

	var securityAttributes windows.SecurityAttributes
	var securityPointer uintptr
	if flags&ucrtONoinherit == 0 {
		securityAttributes.Length = uint32(unsafe.Sizeof(securityAttributes))
		securityAttributes.InheritHandle = 1
		securityPointer = uintptr(unsafe.Pointer(&securityAttributes))
	}
	handle, err := callProc(
		dllKernel32,
		"CreateFileW",
		pathname,
		uintptr(desiredAccess),
		uintptr(shareMode),
		securityPointer,
		uintptr(creationDisposition),
		uintptr(attributes),
		0,
	)
	if handle == ^uintptr(0) {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_PARAMETER))
		return -1
	}
	_, fd := moderncWrapFdHandle(windows.Handle(handle))
	setWindowsDescriptorMode(fd, windowsOpenTextMode(flags))
	return fd
}

func _ccgo_open(tls *libc.TLS, pathname uintptr, flags int32, args uintptr) int32 {
	if pathname == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	wide, err := windows.UTF16FromString(libc.GoString(pathname))
	if err != nil {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return _ccgo__wopen(tls, uintptr(unsafe.Pointer(&wide[0])), flags, args)
}

func tmZone(tmv *Ttm) (string, int) {
	date := time.Date(int(tmv.Ftm_year)+1900, time.Month(tmv.Ftm_mon)+1, int(tmv.Ftm_mday), int(tmv.Ftm_hour), int(tmv.Ftm_min), int(tmv.Ftm_sec), 0, time.Local)
	name, offset := date.Zone()
	return name, offset
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

//go:linkname moderncAddFile modernc.org/libc.addFile
func moderncAddFile(handle windows.Handle, fd int32) uintptr

//go:linkname moderncRemoveFile modernc.org/libc.remFile
func moderncRemoveFile(file *moderncWindowsFile)

func fdHandle(tls *libc.TLS, fd int32) (windows.Handle, bool) {
	f, ok := moderncFdToFile(fd)
	if !ok {
		setCRTWinError(tls, windows.ERROR_INVALID_HANDLE, errorInvalidHandle)
		return 0, false
	}
	return f.handle, true
}

func _ccgo_close(tls *libc.TLS, fd int32) int32 {
	f, ok := moderncFdToFile(fd)
	if !ok {
		setCRTWinError(tls, windows.ERROR_INVALID_HANDLE, errorInvalidHandle)
		return -1
	}
	moderncRemoveFile(f)
	removeWindowsDescriptorMode(fd)
	if err := windows.CloseHandle(f.handle); err != nil {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_HANDLE))
		return -1
	}
	return 0
}

func _ccgo_read(tls *libc.TLS, fd int32, buf uintptr, count uint32) int32 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	mode := windowsDescriptorModeFor(fd)
	if mode.text && mode.eof {
		return 0
	}
	data := cBytes(buf, uint64(count))
	n, err := windows.Read(handle, data)
	if err == windows.ERROR_BROKEN_PIPE {
		return 0
	}
	if err != nil {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_PARAMETER))
		return -1
	}
	if !mode.text {
		return int32(n)
	}

	written := 0
	for i := 0; i < n; i++ {
		switch data[i] {
		case 0x1a:
			setWindowsDescriptorEOF(fd, true)
			return int32(written)
		case '\r':
			if i+1 < n && data[i+1] == '\n' {
				i++
				data[written] = '\n'
				written++
				continue
			}
		}
		data[written] = data[i]
		written++
	}
	return int32(written)
}

func _ccgo_write(tls *libc.TLS, fd int32, buf uintptr, count uint32) int32 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	data := cBytes(buf, uint64(count))
	if windowsDescriptorModeFor(fd).text && bytes.Contains(data, []byte{'\n'}) {
		translated := make([]byte, 0, len(data)+bytes.Count(data, []byte{'\n'}))
		for _, value := range data {
			if value == '\n' {
				translated = append(translated, '\r')
			}
			translated = append(translated, value)
		}
		data = translated
	}
	_, err := windows.Write(handle, data)
	if err != nil {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_PARAMETER))
		return -1
	}
	return int32(count)
}

func _ccgo_lseek(tls *libc.TLS, fd int32, offset int64, whence int32) int64 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	if whence < int32(windows.FILE_BEGIN) || whence > int32(windows.FILE_END) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	var result int64
	if err := setFilePointerRaw(handle, offset, uintptr(unsafe.Pointer(&result)), uint32(whence)); err != nil {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_PARAMETER))
		return -1
	}
	setWindowsDescriptorEOF(fd, false)
	return result
}

func _ccgo__lseeki64(tls *libc.TLS, fd int32, offset int64, whence int32) int64 {
	return _ccgo_lseek(tls, fd, offset, whence)
}

func _ccgo__commit(tls *libc.TLS, fd int32) int32 {
	handle, ok := fdHandle(tls, fd)
	if !ok {
		return -1
	}
	if err := windows.FlushFileBuffers(handle); err != nil {
		setCRTWinError(tls, err, uint32(windows.ERROR_INVALID_HANDLE))
		return -1
	}
	return 0
}

func _ccgo__setmode(tls *libc.TLS, fd, mode int32) int32 {
	if _, ok := fdHandle(tls, fd); !ok {
		return -1
	}
	const (
		ucrtOText   = int32(0x4000)
		ucrtOBinary = int32(0x8000)
	)
	if mode != ucrtOText && mode != ucrtOBinary {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	previous := ucrtOBinary
	if windowsDescriptorModeFor(fd).text {
		previous = ucrtOText
	}
	setWindowsDescriptorMode(fd, mode == ucrtOText)
	return previous
}

func _ccgo__wstat64i32(tls *libc.TLS, path, buffer uintptr) int32 {
	return int32(callUCRTWithCRTErrors(tls, "_wstat64i32", path, buffer))
}

// A modernc Windows descriptor already owns a FILE-like object. fdopen only
// needs to expose that object's token; the duplicated descriptor passed by
// CPython gives fclose the expected ownership semantics.
func _ccgo_fdopen(tls *libc.TLS, fd int32, mode uintptr) uintptr {
	file, ok := moderncFdToFile(fd)
	if !ok {
		setErrno(tls, int32(errno.EBADF))
		return 0
	}
	return file.token
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
	setWindowsDescriptorMode(fd, windowsOpenTextMode(flags))
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
	copyWindowsDescriptorMode(fd, result)
	return result
}

// modernc's Windows dup2 is an unconditional TODO. DuplicateHandle gives the
// target descriptor independent ownership, then the private descriptor-table
// helpers install it at the exact number required by dup2. CPython applies a
// requested non-inheritable setting immediately after this call.
func _ccgo_dup2(tls *libc.TLS, oldfd, newfd int32) int32 {
	if newfd < 0 {
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	source, ok := moderncFdToFile(oldfd)
	if !ok {
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	if oldfd == newfd {
		return newfd
	}

	current := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(current, source.handle, current, &duplicate, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		setErrno(tls, int32(errno.EBADF))
		return -1
	}
	if target, exists := moderncFdToFile(newfd); exists {
		moderncRemoveFile(target)
		_ = windows.CloseHandle(target.handle)
	}
	moderncAddFile(duplicate, newfd)
	copyWindowsDescriptorMode(oldfd, newfd)
	return newfd
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

func _ccgo_isalnum(tls *libc.TLS, value int32) int32 {
	r, _ := callProc(dllUCRT, "isalnum", uintptr(value))
	return int32(r)
}

func _ccgo_tolower(tls *libc.TLS, value int32) int32 {
	r, _ := callProc(dllUCRT, "tolower", uintptr(value))
	return int32(r)
}

func _ccgo_toupper(tls *libc.TLS, value int32) int32 {
	r, _ := callProc(dllUCRT, "toupper", uintptr(value))
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
	// Go's time.Local does not use the same Windows CRT timezone state exposed
	// through __timezone/__daylight. Let UCRT fill its ABI-compatible struct tm
	// so time.localtime(), time.timezone, DST, and tm_gmtoff agree.
	return int32(callUCRTWithErrno(tls, "_localtime64_s", dst, source))
}

func _gmtime_s(tls *libc.TLS, dst, source uintptr) int32 {
	if dst == 0 || source == 0 {
		return int32(errno.EINVAL)
	}
	return int32(callUCRTWithErrno(tls, "_gmtime64_s", dst, source))
}

func _ccgo_mktime(tls *libc.TLS, tm uintptr) int64 {
	return int64(callUCRTWithErrno(tls, "_mktime64", tm))
}

func _ccgo_tzset(tls *libc.TLS) {
	_, _ = callProc(dllUCRT, "_tzset")
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

func _ccgo_strftime(tls *libc.TLS, dst uintptr, capacity uint64, format, tm uintptr) uint64 {
	r, _ := callProc(dllUCRT, "strftime", dst, uintptr(capacity), format, tm)
	return uint64(r)
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

func supportedWindowsSignal(number int32) bool {
	switch number {
	case 2, 4, 8, 11, 15, 21, 22: // SIGINT, SIGILL, SIGFPE, SIGSEGV, SIGTERM, SIGBREAK, SIGABRT
		return true
	default:
		return false
	}
}

func _signal(tls *libc.TLS, number int32, handler uintptr) uintptr {
	if !supportedWindowsSignal(number) {
		setErrno(tls, int32(errno.EINVAL))
		return ^uintptr(0) // SIG_ERR
	}
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
	// Windows spawnv joins an already-quoted argv into a command line. Go's
	// os/exec quotes argv itself, so remove the one syntactic quote pair that
	// CPython's Windows tests (and callers) supply to spawnv.
	for i, arg := range args {
		if len(arg) >= 2 && arg[0] == '"' && arg[len(arg)-1] == '"' {
			args[i] = arg[1 : len(arg)-1]
		}
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

// modernc's Windows SetHandleInformation is an unconditional TODO panic.
func _ccgo_SetHandleInformation(tls *libc.TLS, handle uintptr, mask, flags uint32) int32 {
	if err := windows.SetHandleInformation(windows.Handle(handle), mask, flags); err != nil {
		setWinError(tls, err, errorInvalidHandle)
		return 0
	}
	return 1
}

// modernc's Windows GetOverlappedResult is an unconditional TODO panic.
func _ccgo_GetOverlappedResult(tls *libc.TLS, handle, overlapped, transferred uintptr, wait int32) int32 {
	err := windows.GetOverlappedResult(
		windows.Handle(handle),
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*uint32)(unsafe.Pointer(transferred)),
		wait != 0,
	)
	if err != nil {
		setWinError(tls, err, errorInvalidFunction)
		return 0
	}
	return 1
}

func _ccgo_CreateFileMappingA(tls *libc.TLS, file, securityAttributes uintptr, protect, maximumSizeHigh, maximumSizeLow uint32, name uintptr) uintptr {
	result, err := callProc(
		dllKernel32,
		"CreateFileMappingA",
		file,
		securityAttributes,
		uintptr(protect),
		uintptr(maximumSizeHigh),
		uintptr(maximumSizeLow),
		name,
	)
	if result == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return result
}

// CreateFileMappingW is one of the unusual Win32 calls whose last-error value
// is meaningful on success (ERROR_ALREADY_EXISTS). mmap.resize reads it
// unconditionally, so overwrite stale CRT errno as well as the independent
// Win32 slot on every call.
func _ccgo_CreateFileMappingW(tls *libc.TLS, file, securityAttributes uintptr, protect, maximumSizeHigh, maximumSizeLow uint32, name uintptr) uintptr {
	result, err := callProc(
		dllKernel32,
		"CreateFileMappingW",
		file,
		securityAttributes,
		uintptr(protect),
		uintptr(maximumSizeHigh),
		uintptr(maximumSizeLow),
		name,
	)
	value := winErrno(err, 0)
	if result == 0 && value == 0 {
		value = errorInvalidParam
	}
	tls.SetLastError(value)
	setErrno(tls, int32(value))
	return result
}

func _ccgo_CreateMutexW(tls *libc.TLS, securityAttributes uintptr, initialOwner int32, name uintptr) uintptr {
	result, err := callProc(dllKernel32, "CreateMutexW", securityAttributes, uintptr(uint32(initialOwner)), name)
	if result == 0 {
		setWinError(tls, err, errorInvalidParam)
	}
	return result
}

func _ccgo_ExitProcess(tls *libc.TLS, exitCode uint32) int32 {
	_, _ = callProc(dllKernel32, "ExitProcess", uintptr(exitCode))
	return 0
}

func _ccgo_GetUserNameW(tls *libc.TLS, buffer, size uintptr) int32 {
	return boolProc(tls, dllAdvapi32, "GetUserNameW", buffer, size)
}

func _ccgo_RaiseException(tls *libc.TLS, exceptionCode, exceptionFlags, argumentCount uint32, arguments uintptr) {
	_, _ = callProc(
		dllKernel32,
		"RaiseException",
		uintptr(exceptionCode),
		uintptr(exceptionFlags),
		uintptr(argumentCount),
		arguments,
	)
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
	// Generated C frames live on modernc's TLS stack, not on the native OS
	// thread stack.  _ccgo_frame_address reports positions in this virtual
	// range, so CPython's recursion limits must be initialized from the same
	// range or every comparison is false and recursive C calls can exhaust the
	// Go goroutine stack.
	*(*uintptr)(unsafe.Pointer(low)) = virtualCStackTop - uintptr(virtualCStackSize)
	*(*uintptr)(unsafe.Pointer(high)) = virtualCStackTop
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

type windowsRegisteredWait struct {
	callback uintptr
	context  uintptr
	flags    uint32
}

var (
	windowsRegisteredWaitMu   sync.Mutex
	windowsRegisteredWaits    = map[uintptr]windowsRegisteredWait{}
	windowsRegisteredWaitNext atomic.Uintptr
	windowsWaitCallback       = syscall.NewCallback(windowsRegisteredWaitCallback)
)

func windowsRegisteredWaitCallback(token, timerOrWaitFired uintptr) uintptr {
	windowsRegisteredWaitMu.Lock()
	state, ok := windowsRegisteredWaits[token]
	if ok && state.flags&0x8 != 0 { // WT_EXECUTEONLYONCE
		delete(windowsRegisteredWaits, token)
	}
	windowsRegisteredWaitMu.Unlock()
	if !ok {
		return 0
	}

	// ccgo function pointers are Go function values, not native addresses. The
	// native thread-pool callback above is a fixed Go trampoline; dispatch the
	// original translated callback from Go with a TLS owned by this callback.
	dtls := libc.NewTLS()
	defer dtls.Close()
	callback := *(*func(*libc.TLS, uintptr, uint8))(unsafe.Pointer(&struct{ uintptr }{state.callback}))
	callback(dtls, state.context, uint8(timerOrWaitFired))
	return 0
}

func _RegisterWaitForSingleObject(tls *libc.TLS, result, object, callback, context uintptr, milliseconds, flags uint32) int32 {
	if result == 0 || callback == 0 {
		setWinError(tls, windows.ERROR_INVALID_PARAMETER, errorInvalidParam)
		return 0
	}

	token := windowsRegisteredWaitNext.Add(1)
	windowsRegisteredWaitMu.Lock()
	windowsRegisteredWaits[token] = windowsRegisteredWait{callback: callback, context: context, flags: flags}
	windowsRegisteredWaitMu.Unlock()

	r, err := callProc(
		dllKernel32,
		"RegisterWaitForSingleObject",
		result,
		object,
		windowsWaitCallback,
		token,
		uintptr(milliseconds),
		uintptr(flags),
	)
	if r == 0 {
		windowsRegisteredWaitMu.Lock()
		delete(windowsRegisteredWaits, token)
		windowsRegisteredWaitMu.Unlock()
		setWinError(tls, err, errorInvalidParam)
		return 0
	}
	return 1
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

const (
	hresultInvalidArgument   = -2147024809 // HRESULT_FROM_WIN32(ERROR_INVALID_PARAMETER)
	hresultInsufficientSpace = -2147024774 // HRESULT_FROM_WIN32(ERROR_INSUFFICIENT_BUFFER)
)

func winPathSeparator(c uint16) bool { return c == '\\' || c == '/' }

func asciiLetter(c uint16) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func asciiWideEqualFold(value []uint16, text string) bool {
	if len(value) != len(text) {
		return false
	}
	for i, c := range value {
		want := uint16(text[i])
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if want >= 'a' && want <= 'z' {
			want -= 'a' - 'A'
		}
		if c != want {
			return false
		}
	}
	return true
}

func uncRootEnd(path []uint16, start int) (int, bool) {
	serverStart := start
	for start < len(path) && !winPathSeparator(path[start]) {
		start++
	}
	if start == serverStart || start == len(path) {
		return 0, false
	}
	for start < len(path) && winPathSeparator(path[start]) {
		start++
	}
	shareStart := start
	for start < len(path) && !winPathSeparator(path[start]) {
		start++
	}
	if start == shareStart {
		return 0, false
	}
	if start < len(path) {
		start++
	}
	return start, true
}

func winRootEnd(path []uint16) (int, bool) {
	if len(path) == 0 {
		return 0, false
	}
	if len(path) >= 4 && winPathSeparator(path[0]) && winPathSeparator(path[1]) &&
		(path[2] == '?' || path[2] == '.') && winPathSeparator(path[3]) {
		if len(path) >= 8 && asciiWideEqualFold(path[4:7], "UNC") && winPathSeparator(path[7]) {
			return uncRootEnd(path, 8)
		}
		if len(path) >= 6 && asciiLetter(path[4]) && path[5] == ':' {
			if len(path) >= 7 && winPathSeparator(path[6]) {
				return 7, true
			}
			return 6, true
		}
		end := 4
		for end < len(path) && !winPathSeparator(path[end]) {
			end++
		}
		if end == 4 {
			return 0, false
		}
		if end < len(path) {
			end++
		}
		return end, true
	}
	if len(path) >= 2 && winPathSeparator(path[0]) && winPathSeparator(path[1]) {
		return uncRootEnd(path, 2)
	}
	if len(path) >= 2 && asciiLetter(path[0]) && path[1] == ':' {
		if len(path) >= 3 && winPathSeparator(path[2]) {
			return 3, true
		}
		return 2, true
	}
	if winPathSeparator(path[0]) {
		return 1, true
	}
	return 0, false
}

func _PathCchCombineEx(tls *libc.TLS, output uintptr, capacity uint64, first, second uintptr, flags uint32) int32 {
	if output == 0 || capacity == 0 || first == 0 || second == 0 {
		return hresultInvalidArgument
	}
	base, more := wideString(first), wideString(second)
	var combined string
	if filepath.IsAbs(more) || filepath.VolumeName(more) != "" {
		combined = filepath.Clean(more)
	} else if base == "" {
		combined = more
	} else if more == "" {
		combined = base
	} else {
		combined = filepath.Join(base, more)
	}
	if _, ok := writeWide(output, capacity, combined); !ok {
		*(*uint16)(unsafe.Pointer(output)) = 0
		return hresultInsufficientSpace
	}
	return 0
}

func _PathCchSkipRoot(tls *libc.TLS, path, result uintptr) int32 {
	if path == 0 || result == 0 {
		return hresultInvalidArgument
	}
	end, ok := winRootEnd(readWide(path))
	if !ok {
		*(*uintptr)(unsafe.Pointer(result)) = path
		return hresultInvalidArgument
	}
	*(*uintptr)(unsafe.Pointer(result)) = path + uintptr(end)*2
	return 0
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
	r, err := callProc(dllIPHlp, "if_nametoindex", name)
	if r == 0 {
		setWSAErrorFrom(tls, err)
	}
	return uint32(r)
}

func _if_indextoname(tls *libc.TLS, index uint32, name uintptr) uintptr {
	r, err := callProc(dllIPHlp, "if_indextoname", uintptr(index), name)
	if r == 0 {
		setWSAErrorFrom(tls, err)
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

func recordWSAError(tls *libc.TLS, value uint32) {
	if value == 0 {
		value = errorInvalidFunction
	}
	tls.SetLastError(value)
	setErrno(tls, int32(value))
}

func setWSAError(tls *libc.TLS) {
	r, _ := callProc(dllWS2, "WSAGetLastError")
	recordWSAError(tls, uint32(r))
}

// A syscall's returned errno is captured while the goroutine is still on the
// calling OS thread. Prefer it to a later WSAGetLastError call, which could run
// after the goroutine has migrated to another thread.
func setWSAErrorFrom(tls *libc.TLS, err error) {
	if value := winErrno(err, 0); value != 0 {
		recordWSAError(tls, value)
		return
	}
	setWSAError(tls)
}

func socketResult(tls *libc.TLS, result uintptr, err error) int32 {
	value := int32(result)
	if value == socketError {
		setWSAErrorFrom(tls, err)
	}
	return value
}

func _ccgo_WSAGetLastError(tls *libc.TLS) int32 {
	return int32(tls.GetLastError())
}

func _ccgo_WSASetLastError(tls *libc.TLS, value int32) {
	_WSASetLastError(tls, value)
}

func _ccgo_time(tls *libc.TLS, location uintptr) int64 {
	now := time.Now().Unix()
	if location != 0 {
		*(*int64)(unsafe.Pointer(location)) = now
	}
	return now
}

func _ccgo_RegOpenKeyExW(tls *libc.TLS, key, subkey uintptr, options, access uint32, result uintptr) int32 {
	err := windows.RegOpenKeyEx(
		windows.Handle(key),
		u16ptr(subkey),
		options,
		access,
		(*windows.Handle)(unsafe.Pointer(result)),
	)
	if err == nil {
		return 0
	}
	return int32(winErrno(err, errorInvalidFunction))
}

func registryStatus(dllName string, arguments ...uintptr) int32 {
	result, _ := callProc(dllAdvapi32, dllName, arguments...)
	return int32(result)
}

func _ccgo_RegCloseKey(tls *libc.TLS, key uintptr) int32 {
	return registryStatus("RegCloseKey", key)
}

func _ccgo_RegConnectRegistryW(tls *libc.TLS, computerName, key, result uintptr) int32 {
	return registryStatus("RegConnectRegistryW", computerName, key, result)
}

func _ccgo_RegCreateKeyExW(tls *libc.TLS, key, subkey uintptr, reserved uint32, class uintptr, options, access uint32, securityAttributes, result, disposition uintptr) int32 {
	return registryStatus(
		"RegCreateKeyExW",
		key,
		subkey,
		uintptr(reserved),
		class,
		uintptr(options),
		uintptr(access),
		securityAttributes,
		result,
		disposition,
	)
}

func _ccgo_RegDeleteKeyW(tls *libc.TLS, key, subkey uintptr) int32 {
	return registryStatus("RegDeleteKeyW", key, subkey)
}

func _ccgo_RegDeleteValueW(tls *libc.TLS, key, value uintptr) int32 {
	return registryStatus("RegDeleteValueW", key, value)
}

func _ccgo_RegEnumKeyExW(tls *libc.TLS, key uintptr, index uint32, name, nameLength, reserved, class, classLength, lastWriteTime uintptr) int32 {
	return registryStatus(
		"RegEnumKeyExW",
		key,
		uintptr(index),
		name,
		nameLength,
		reserved,
		class,
		classLength,
		lastWriteTime,
	)
}

func _ccgo_RegEnumValueW(tls *libc.TLS, key uintptr, index uint32, valueName, valueNameLength, reserved, valueType, data, dataLength uintptr) int32 {
	return registryStatus(
		"RegEnumValueW",
		key,
		uintptr(index),
		valueName,
		valueNameLength,
		reserved,
		valueType,
		data,
		dataLength,
	)
}

func _ccgo_RegQueryValueExW(tls *libc.TLS, key, valueName, reserved, valueType, data, dataLength uintptr) int32 {
	return registryStatus("RegQueryValueExW", key, valueName, reserved, valueType, data, dataLength)
}

func _ccgo_RegSetValueExW(tls *libc.TLS, key, valueName uintptr, reserved, valueType uint32, data uintptr, dataLength uint32) int32 {
	return registryStatus(
		"RegSetValueExW",
		key,
		valueName,
		uintptr(reserved),
		uintptr(valueType),
		data,
		uintptr(dataLength),
	)
}

func _ccgo_MessageBeep(tls *libc.TLS, kind uint32) int32 {
	return boolProc(tls, dllUser32, "MessageBeep", uintptr(kind))
}

func _ccgo_abort(tls *libc.TLS) {
	callUCRTWithErrno(tls, "abort")
}

func _ccgo_raise(tls *libc.TLS, number int32) int32 {
	if !supportedWindowsSignal(number) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	signalMu.Lock()
	handler := windowsSignals[number]
	signalMu.Unlock()

	switch handler {
	case 1: // SIG_IGN
		return 0
	case 0: // SIG_DFL
		if number == 21 { // UCRT raise() does not accept SIGBREAK.
			setErrno(tls, int32(errno.EINVAL))
			return -1
		}
		return int32(callUCRTWithErrno(tls, "raise", uintptr(number)))
	default:
		(*(*func(*libc.TLS, int32))(unsafe.Pointer(&struct{ uintptr }{handler})))(tls, number)
		return 0
	}
}

type winsockExtensionProc struct {
	once    sync.Once
	address uintptr
	err     error
}

var (
	connectExProc     winsockExtensionProc
	disconnectExProc  winsockExtensionProc
	wsaidDisconnectEx = windows.GUID{
		Data1: 0x7fda2e11,
		Data2: 0x8630,
		Data3: 0x436f,
		Data4: [8]byte{0xa0, 0x31, 0xf5, 0x36, 0xa6, 0xee, 0xc1, 0x57},
	}
)

func loadWinsockExtension(extension *winsockExtensionProc, guid *windows.GUID) (uintptr, error) {
	extension.once.Do(func() {
		socket, err := windows.Socket(windows.AF_INET, windows.SOCK_STREAM, windows.IPPROTO_TCP)
		if err != nil {
			extension.err = err
			return
		}
		defer windows.Closesocket(socket)

		var bytesReturned uint32
		extension.err = windows.WSAIoctl(
			socket,
			windows.SIO_GET_EXTENSION_FUNCTION_POINTER,
			(*byte)(unsafe.Pointer(guid)),
			uint32(unsafe.Sizeof(*guid)),
			(*byte)(unsafe.Pointer(&extension.address)),
			uint32(unsafe.Sizeof(extension.address)),
			&bytesReturned,
			nil,
			0,
		)
	})
	return extension.address, extension.err
}

// CPython normally calls these Winsock extension functions through pointers
// returned by WSAIoctl. A native function address is not a ccgo Go function
// pointer, so the ccgo-only overlapped.c patch routes the calls through these
// wrappers and syscall.SyscallN (or x/sys direct DLL wrappers) instead.
func _ccgo_AcceptEx(tls *libc.TLS, listenSocket, acceptSocket uint64, output uintptr, receiveDataLength, localAddressLength, remoteAddressLength uint32, bytesReceived, overlapped uintptr) int32 {
	err := windows.AcceptEx(
		windows.Handle(listenSocket),
		windows.Handle(acceptSocket),
		byteptr(output),
		receiveDataLength,
		localAddressLength,
		remoteAddressLength,
		(*uint32)(unsafe.Pointer(bytesReceived)),
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
	)
	if err != nil {
		setWSAErrorFrom(tls, err)
		return 0
	}
	return 1
}

func _ccgo_ConnectEx(tls *libc.TLS, socket uint64, address uintptr, addressLength int32, sendBuffer uintptr, sendDataLength uint32, bytesSent, overlapped uintptr) int32 {
	procedure, err := loadWinsockExtension(&connectExProc, &windows.WSAID_CONNECTEX)
	if err != nil {
		setWSAErrorFrom(tls, err)
		return 0
	}
	result, _, err := syscall.SyscallN(
		procedure,
		uintptr(socket),
		address,
		uintptr(uint32(addressLength)),
		sendBuffer,
		uintptr(sendDataLength),
		bytesSent,
		overlapped,
	)
	if result == 0 {
		setWSAErrorFrom(tls, err)
		return 0
	}
	return 1
}

func _ccgo_DisconnectEx(tls *libc.TLS, socket uint64, overlapped uintptr, flags, reserved uint32) int32 {
	procedure, err := loadWinsockExtension(&disconnectExProc, &wsaidDisconnectEx)
	if err != nil {
		setWSAErrorFrom(tls, err)
		return 0
	}
	result, _, err := syscall.SyscallN(
		procedure,
		uintptr(socket),
		overlapped,
		uintptr(flags),
		uintptr(reserved),
	)
	if result == 0 {
		setWSAErrorFrom(tls, err)
		return 0
	}
	return 1
}

func _ccgo_TransmitFile(tls *libc.TLS, socket uint64, file uintptr, bytesToWrite, bytesPerSend uint32, overlapped, transmitBuffers uintptr, flags uint32) int32 {
	err := windows.TransmitFile(
		windows.Handle(socket),
		windows.Handle(file),
		bytesToWrite,
		bytesPerSend,
		(*windows.Overlapped)(unsafe.Pointer(overlapped)),
		(*windows.TransmitFileBuffers)(unsafe.Pointer(transmitBuffers)),
		flags,
	)
	if err != nil {
		setWSAErrorFrom(tls, err)
		return 0
	}
	return 1
}

func _ccgo_getservbyname(tls *libc.TLS, name, protocol uintptr) uintptr {
	result, err := callProc(dllWS2, "getservbyname", name, protocol)
	if result == 0 {
		setWSAErrorFrom(tls, err)
	}
	return result
}

func _ccgo_inet_ntoa(tls *libc.TLS, address Tin_addr) uintptr {
	value := *(*uint32)(unsafe.Pointer(&address))
	result, err := callProc(dllWS2, "inet_ntoa", uintptr(value))
	if result == 0 {
		setWSAErrorFrom(tls, err)
	}
	return result
}

func _ccgo_socket(tls *libc.TLS, family, socketType, protocol int32) uint64 {
	handle, err := windows.Socket(int(family), int(socketType), int(protocol))
	if err != nil {
		setWSAErrorFrom(tls, err)
		return invalidSocket
	}
	return uint64(handle)
}

func _ccgo_bind(tls *libc.TLS, socket uint64, address uintptr, addressLength int32) int32 {
	result, err := callProc(dllWS2, "bind", uintptr(socket), address, uintptr(uint32(addressLength)))
	return socketResult(tls, result, err)
}

func _ccgo_connect(tls *libc.TLS, socket uint64, address uintptr, addressLength int32) int32 {
	result, err := callProc(dllWS2, "connect", uintptr(socket), address, uintptr(uint32(addressLength)))
	return socketResult(tls, result, err)
}

func _ccgo_listen(tls *libc.TLS, socket uint64, backlog int32) int32 {
	if err := windows.Listen(windows.Handle(socket), int(backlog)); err != nil {
		setWSAErrorFrom(tls, err)
		return socketError
	}
	return 0
}

func _ccgo_accept(tls *libc.TLS, socket uint64, address, addressLength uintptr) uint64 {
	result, err := callProc(dllWS2, "accept", uintptr(socket), address, addressLength)
	accepted := uint64(result)
	if accepted == invalidSocket {
		setWSAErrorFrom(tls, err)
	}
	return accepted
}

func _ccgo_getpeername(tls *libc.TLS, socket uint64, address, addressLength uintptr) int32 {
	result, err := callProc(dllWS2, "getpeername", uintptr(socket), address, addressLength)
	return socketResult(tls, result, err)
}

func _ccgo_getsockname(tls *libc.TLS, socket uint64, address, addressLength uintptr) int32 {
	result, err := callProc(dllWS2, "getsockname", uintptr(socket), address, addressLength)
	return socketResult(tls, result, err)
}

func _ccgo_recv(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32) int32 {
	result, err := callProc(dllWS2, "recv", uintptr(socket), buffer, uintptr(uint32(length)), uintptr(uint32(flags)))
	return socketResult(tls, result, err)
}

func _ccgo_send(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32) int32 {
	result, err := callProc(dllWS2, "send", uintptr(socket), buffer, uintptr(uint32(length)), uintptr(uint32(flags)))
	return socketResult(tls, result, err)
}

func _ccgo_recvfrom(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32, from, fromLength uintptr) int32 {
	result, err := callProc(dllWS2, "recvfrom", uintptr(socket), buffer, uintptr(uint32(length)), uintptr(uint32(flags)), from, fromLength)
	return socketResult(tls, result, err)
}

func _ccgo_sendto(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32, to uintptr, toLength int32) int32 {
	result, err := callProc(dllWS2, "sendto", uintptr(socket), buffer, uintptr(uint32(length)), uintptr(uint32(flags)), to, uintptr(uint32(toLength)))
	return socketResult(tls, result, err)
}

func _ccgo_shutdown(tls *libc.TLS, socket uint64, how int32) int32 {
	if err := windows.Shutdown(windows.Handle(socket), int(how)); err != nil {
		setWSAErrorFrom(tls, err)
		return socketError
	}
	return 0
}

func _ccgo_sscanf(tls *libc.TLS, input, format, args uintptr) int32 {
	// This is the sole sscanf call in the generated Windows archive. CPython
	// uses it to parse a six-byte Bluetooth address and a seventh conversion
	// to reject trailing input. modernc's scanner panics on the final %c.
	if libc.GoString(format) != "%X:%X:%X:%X:%X:%X%c" {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	var values [6]uint32
	var trailing rune
	converted, _ := fmt.Sscanf(
		libc.GoString(input),
		"%X:%X:%X:%X:%X:%X%c",
		&values[0], &values[1], &values[2],
		&values[3], &values[4], &values[5], &trailing,
	)
	for index := range values {
		destination := libc.VaUintptr(&args)
		if index < converted {
			*(*uint32)(unsafe.Pointer(destination)) = values[index]
		}
	}
	trailingDestination := libc.VaUintptr(&args)
	if converted == 7 {
		*(*int8)(unsafe.Pointer(trailingDestination)) = int8(trailing)
	}
	return int32(converted)
}

func _ccgo_getsockopt(tls *libc.TLS, socket uint64, level, option int32, value, valueLength uintptr) int32 {
	if err := windows.Getsockopt(
		windows.Handle(socket),
		level,
		option,
		byteptr(value),
		(*int32)(unsafe.Pointer(valueLength)),
	); err != nil {
		setWSAErrorFrom(tls, err)
		return socketError
	}
	return 0
}

func _ccgo_setsockopt(tls *libc.TLS, socket uint64, level, option int32, value uintptr, valueLength int32) int32 {
	if err := windows.Setsockopt(windows.Handle(socket), level, option, byteptr(value), valueLength); err != nil {
		setWSAErrorFrom(tls, err)
		return socketError
	}
	return 0
}

// Winsock's fd_set is a uint32 count, four bytes of alignment padding, then
// an array of 64-bit SOCKET values on both supported targets. The generated
// Tfd_set/Tfd_set1 layouts match that ABI, so pass their storage through.
func _ccgo_select(tls *libc.TLS, nfds int32, readfds, writefds, exceptfds, timeout uintptr) int32 {
	result, err := callProc(
		dllWS2,
		"select",
		uintptr(uint32(nfds)),
		readfds,
		writefds,
		exceptfds,
		timeout,
	)
	return socketResult(tls, result, err)
}

func _ccgo_ioctlsocket(tls *libc.TLS, socket uint64, command int32, argument uintptr) int32 {
	result, err := callProc(dllWS2, "ioctlsocket", uintptr(socket), uintptr(uint32(command)), argument)
	return socketResult(tls, result, err)
}

func _ccgo_closesocket(tls *libc.TLS, socket uint64) int32 {
	if err := windows.Closesocket(windows.Handle(socket)); err != nil {
		setWSAErrorFrom(tls, err)
		return socketError
	}
	return 0
}

func _WSASetLastError(tls *libc.TLS, value int32) {
	_, _ = callProc(dllWS2, "WSASetLastError", uintptr(value))
	tls.SetLastError(uint32(value))
	setErrno(tls, value)
}

func _WSACleanup(tls *libc.TLS) int32 {
	if err := windows.WSACleanup(); err != nil {
		setWSAErrorFrom(tls, err)
		return -1
	}
	return 0
}

func _WSAConnect(tls *libc.TLS, socket uint64, address uintptr, addressLength int32, callerData, calleeData, sqos, gqos uintptr) int32 {
	r, err := callProc(dllWS2, "WSAConnect", uintptr(socket), address, uintptr(uint32(addressLength)), callerData, calleeData, sqos, gqos)
	if int32(r) == -1 {
		setWSAErrorFrom(tls, err)
	}
	return int32(r)
}

func _WSADuplicateSocketW(tls *libc.TLS, socket uint64, processID uint32, info uintptr) int32 {
	err := windows.WSADuplicateSocket(windows.Handle(socket), processID, (*windows.WSAProtocolInfo)(unsafe.Pointer(info)))
	if err != nil {
		setWSAErrorFrom(tls, err)
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
		setWSAErrorFrom(tls, err)
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
		setWSAErrorFrom(tls, err)
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
		setWSAErrorFrom(tls, err)
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
		setWSAErrorFrom(tls, err)
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
		setWSAErrorFrom(tls, err)
		return -1
	}
	return 0
}

func _WSASocketW(tls *libc.TLS, family, socketType, protocol int32, info uintptr, group, flags uint32) uint64 {
	handle, err := windows.WSASocket(family, socketType, protocol, (*windows.WSAProtocolInfo)(unsafe.Pointer(info)), group, flags)
	if err != nil {
		setWSAErrorFrom(tls, err)
		return invalidSocket
	}
	return uint64(handle)
}

func _WSAStringToAddressW(tls *libc.TLS, text uintptr, family int32, info, address, length uintptr) int32 {
	r, err := callProc(dllWS2, "WSAStringToAddressW", text, uintptr(uint32(family)), info, address, length)
	if int32(r) == -1 {
		setWSAErrorFrom(tls, err)
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
	return _ccgo_recvfrom(tls, socket, buffer, length, flags, from, fromLength)
}

func _sendto(tls *libc.TLS, socket uint64, buffer uintptr, length, flags int32, to uintptr, toLength int32) int32 {
	return _ccgo_sendto(tls, socket, buffer, length, flags, to, toLength)
}

func _gethostbyaddr(tls *libc.TLS, address uintptr, length, family int32) uintptr {
	r, err := callProc(dllWS2, "gethostbyaddr", address, uintptr(uint32(length)), uintptr(uint32(family)))
	if r == 0 {
		setWSAErrorFrom(tls, err)
	}
	return r
}

func _gethostbyname(tls *libc.TLS, name uintptr) uintptr {
	r, err := callProc(dllWS2, "gethostbyname", name)
	if r == 0 {
		setWSAErrorFrom(tls, err)
	}
	return r
}

func _getservbyport(tls *libc.TLS, port int32, protocol uintptr) uintptr {
	r, err := callProc(dllWS2, "getservbyport", uintptr(uint32(port)), protocol)
	if r == 0 {
		setWSAErrorFrom(tls, err)
	}
	return r
}

func _getprotobyname(tls *libc.TLS, name uintptr) uintptr {
	r, err := callProc(dllWS2, "getprotobyname", name)
	if r == 0 {
		setWSAErrorFrom(tls, err)
	}
	return r
}

func _inet_addr(tls *libc.TLS, address uintptr) uint32 {
	r, _ := callProc(dllWS2, "inet_addr", address)
	return uint32(r)
}

func _inet_pton(tls *libc.TLS, family int32, source, destination uintptr) int32 {
	r, err := callProc(dllWS2, "inet_pton", uintptr(uint32(family)), source, destination)
	if int32(r) == -1 {
		setWSAErrorFrom(tls, err)
	}
	return int32(r)
}

func _inet_ntop(tls *libc.TLS, family int32, source, destination uintptr, size uint64) uintptr {
	r, err := callProc(dllWS2, "inet_ntop", uintptr(uint32(family)), source, destination, uintptr(size))
	if r == 0 {
		setWSAErrorFrom(tls, err)
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
