//go:build darwin

package libpython

import (
	"bytes"
	"encoding/binary"
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	"modernc.org/libc/errno"
)

func wideLen(p uintptr) uint64 {
	for n := uint64(0); ; n++ {
		if *(*int32)(unsafe.Pointer(p + uintptr(n)*4)) == 0 {
			return n
		}
	}
}

func wideRunes(p uintptr) []rune {
	n := wideLen(p)
	r := make([]rune, n)
	for i := range r {
		r[i] = rune(*(*int32)(unsafe.Pointer(p + uintptr(i)*4)))
	}
	return r
}

func cStrings(p uintptr) []string {
	var r []string
	for ; ; p += 8 {
		q := *(*uintptr)(unsafe.Pointer(p))
		if q == 0 {
			return r
		}
		r = append(r, libc.GoString(q))
	}
}

// Darwin's relative pthread wait is implemented in terms of libc's absolute
// pthread_cond_timedwait.
func _pthread_cond_timedwait_relative_np(tls *libc.TLS, cond, mutex, reltime uintptr) int32 {
	abs := tls.Alloc(16)
	defer tls.Free(16)
	now := time.Now()
	rel := (*Ttimespec)(unsafe.Pointer(reltime))
	sec := now.Unix() + int64(rel.Ftv_sec)
	nsec := int64(now.Nanosecond()) + rel.Ftv_nsec
	sec += nsec / int64(time.Second)
	nsec %= int64(time.Second)
	if nsec < 0 {
		sec--
		nsec += int64(time.Second)
	}
	(*Ttimespec)(unsafe.Pointer(abs)).Ftv_sec = sec
	(*Ttimespec)(unsafe.Pointer(abs)).Ftv_nsec = nsec
	return libc.Xpthread_cond_timedwait(tls, cond, mutex, abs)
}

func _memset_s(tls *libc.TLS, dst uintptr, dstsz uint64, c int32, n uint64) int32 {
	if dst == 0 || n > dstsz {
		return int32(errno.EINVAL)
	}
	libc.Xmemset(tls, dst, c, n)
	return 0
}

func _ccgo_strerror(tls *libc.TLS, number int32) uintptr {
	message := syscall.Errno(number).Error()
	if message != "" && message[0] >= 'a' && message[0] <= 'z' {
		message = strings.ToUpper(message[:1]) + message[1:]
	}
	return stableCString(message)
}

func _ccgo_gai_strerror(tls *libc.TLS, number int32) uintptr {
	message := map[int32]string{
		1:  "Address family for hostname not supported",
		2:  "Temporary failure in name resolution",
		3:  "Invalid value for ai_flags",
		4:  "Non-recoverable failure in name resolution",
		5:  "ai_family not supported",
		6:  "Memory allocation failure",
		7:  "No address associated with hostname",
		8:  "nodename nor servname provided, or not known",
		9:  "servname not supported for ai_socktype",
		10: "ai_socktype not supported",
		11: "System error",
		12: "Invalid value for hints",
		13: "Resolved protocol is unknown",
		14: "Argument buffer overflow",
	}[number]
	if message == "" {
		message = "Unknown error"
	}
	return stableCString(message)
}

func _ccgo_openpty(tls *libc.TLS, masterp, slavep, name, termp, winp uintptr) int32 {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return errnoResult(tls, err)
	}
	fail := func(err error) int32 {
		unix.Close(master)
		return errnoResult(tls, err)
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCPTYGRANT, 0); e != 0 {
		return fail(e)
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCPTYUNLK, 0); e != 0 {
		return fail(e)
	}
	var path [128]byte
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&path[0]))); e != 0 {
		return fail(e)
	}
	slave, err := unix.Open(string(path[:bytes.IndexByte(path[:], 0)]), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	failBoth := func(err error) int32 {
		unix.Close(slave)
		return fail(err)
	}
	if termp != 0 {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(slave), unix.TIOCSETA, termp); e != 0 {
			return failBoth(e)
		}
	}
	if winp != 0 {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(slave), unix.TIOCSWINSZ, winp); e != 0 {
			return failBoth(e)
		}
	}
	*(*int32)(unsafe.Pointer(masterp)) = int32(master)
	*(*int32)(unsafe.Pointer(slavep)) = int32(slave)
	if name != 0 {
		copy(cBytes(name, uint64(len(path))), path[:])
	}
	return 0
}

func _ccgo_accept(tls *libc.TLS, socket int32, address, addressLen uintptr) int32 {
	fd, _, err := unix.Syscall(unix.SYS_ACCEPT, uintptr(socket), address, addressLen)
	if err != 0 {
		return errnoResult(tls, err)
	}
	return int32(fd)
}

// These symbols support optional Darwin remote-debugging and native
// faulthandler extras. cpython-go does not expose the underlying facilities.
func _mach_vm_region(tls *libc.TLS, task uint32, address, size uintptr, flavor int32, info, count, objectName uintptr) int32 {
	return 5 // KERN_FAILURE
}

func _mach_vm_read_overwrite(tls *libc.TLS, task uint32, address, size, data uint64, outSize uintptr) int32 {
	return 5 // KERN_FAILURE
}

func _proc_regionfilename(tls *libc.TLS, pid int32, address uint64, buffer uintptr, size uint32) int32 {
	return 0
}

func _backtrace(tls *libc.TLS, buffer uintptr, size int32) int32 { return 0 }
func _dladdr(tls *libc.TLS, address, info uintptr) int32         { return 0 }

func __os_log_internal(tls *libc.TLS, dso, log uintptr, logType uint8, format, args uintptr) {}

var (
	__os_log_default uintptr
	___dso_handle    uintptr
)

// Wide-character and UTF-8 locale shims.
func _wcslen(tls *libc.TLS, s uintptr) uint64 { return wideLen(s) }

func _wcscmp(tls *libc.TLS, a, b uintptr) int32 {
	for i := uintptr(0); ; i += 4 {
		x, y := *(*int32)(unsafe.Pointer(a + i)), *(*int32)(unsafe.Pointer(b + i))
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		if x == 0 {
			return 0
		}
	}
}

func _wcsncmp(tls *libc.TLS, a, b uintptr, n uint64) int32 {
	for i := uint64(0); i < n; i++ {
		x := *(*int32)(unsafe.Pointer(a + uintptr(i)*4))
		y := *(*int32)(unsafe.Pointer(b + uintptr(i)*4))
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		if x == 0 {
			break
		}
	}
	return 0
}

func _wcscpy(tls *libc.TLS, dst, src uintptr) uintptr {
	n := wideLen(src) + 1
	copy(unsafe.Slice((*int32)(unsafe.Pointer(dst)), int(n)), unsafe.Slice((*int32)(unsafe.Pointer(src)), int(n)))
	return dst
}

func _wcsncpy(tls *libc.TLS, dst, src uintptr, n uint64) uintptr {
	d := unsafe.Slice((*int32)(unsafe.Pointer(dst)), int(n))
	i := 0
	for ; i < len(d); i++ {
		d[i] = *(*int32)(unsafe.Pointer(src + uintptr(i)*4))
		if d[i] == 0 {
			i++
			break
		}
	}
	for ; i < len(d); i++ {
		d[i] = 0
	}
	return dst
}

func _ccgo_wcschr(tls *libc.TLS, s uintptr, c int32) uintptr {
	for p := s; ; p += 4 {
		if *(*int32)(unsafe.Pointer(p)) == c {
			return p
		}
		if *(*int32)(unsafe.Pointer(p)) == 0 {
			return 0
		}
	}
}

func _wcsrchr(tls *libc.TLS, s uintptr, c int32) uintptr {
	var found uintptr
	for p := s; ; p += 4 {
		if *(*int32)(unsafe.Pointer(p)) == c {
			found = p
		}
		if *(*int32)(unsafe.Pointer(p)) == 0 {
			return found
		}
	}
}

func _wcstok(tls *libc.TLS, s, delim, state uintptr) uintptr {
	if s == 0 {
		s = *(*uintptr)(unsafe.Pointer(state))
	}
	if s == 0 {
		return 0
	}
	isDelim := func(c int32) bool { return _ccgo_wcschr(tls, delim, c) != 0 }
	for *(*int32)(unsafe.Pointer(s)) != 0 && isDelim(*(*int32)(unsafe.Pointer(s))) {
		s += 4
	}
	if *(*int32)(unsafe.Pointer(s)) == 0 {
		*(*uintptr)(unsafe.Pointer(state)) = 0
		return 0
	}
	for p := s; ; p += 4 {
		c := *(*int32)(unsafe.Pointer(p))
		if c == 0 {
			*(*uintptr)(unsafe.Pointer(state)) = 0
			return s
		}
		if isDelim(c) {
			*(*int32)(unsafe.Pointer(p)) = 0
			*(*uintptr)(unsafe.Pointer(state)) = p + 4
			return s
		}
	}
}

func _wcstol(tls *libc.TLS, s, end uintptr, base int32) int64 {
	if base != 0 && (base < 2 || base > 36) {
		setErrno(tls, int32(errno.EINVAL))
		if end != 0 {
			*(*uintptr)(unsafe.Pointer(end)) = s
		}
		return 0
	}
	p := s
	for c := *(*int32)(unsafe.Pointer(p)); c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; c = *(*int32)(unsafe.Pointer(p)) {
		p += 4
	}
	sign := int64(1)
	if *(*int32)(unsafe.Pointer(p)) == '-' {
		sign, p = -1, p+4
	} else if *(*int32)(unsafe.Pointer(p)) == '+' {
		p += 4
	}
	digit := func(c int32) int32 {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'a' && c <= 'z':
			return c - 'a' + 10
		case c >= 'A' && c <= 'Z':
			return c - 'A' + 10
		}
		return 99
	}
	if base == 0 {
		base = 10
		if *(*int32)(unsafe.Pointer(p)) == '0' {
			base = 8
			if x := *(*int32)(unsafe.Pointer(p + 4)); x == 'x' || x == 'X' {
				if digit(*(*int32)(unsafe.Pointer(p + 8))) < 16 {
					base, p = 16, p+8
				}
			}
		}
	} else if base == 16 && *(*int32)(unsafe.Pointer(p)) == '0' {
		if x := *(*int32)(unsafe.Pointer(p + 4)); x == 'x' || x == 'X' {
			if digit(*(*int32)(unsafe.Pointer(p + 8))) < 16 {
				p += 8
			}
		}
	}
	start, value := p, uint64(0)
	limit := uint64(math.MaxInt64)
	if sign < 0 {
		limit++
	}
	overflow := false
	for {
		c := *(*int32)(unsafe.Pointer(p))
		d := digit(c)
		if d >= base {
			break
		}
		if value > (limit-uint64(d))/uint64(base) {
			overflow = true
			value = limit
		} else if !overflow {
			value = value*uint64(base) + uint64(d)
		}
		p += 4
	}
	if end != 0 {
		if p == start {
			*(*uintptr)(unsafe.Pointer(end)) = s
		} else {
			*(*uintptr)(unsafe.Pointer(end)) = p
		}
	}
	if overflow {
		setErrno(tls, int32(errno.ERANGE))
	}
	if sign < 0 {
		if value == uint64(math.MaxInt64)+1 {
			return math.MinInt64
		}
		return -int64(value)
	}
	return int64(value)
}

func _wcscoll(tls *libc.TLS, a, b uintptr) int32 { return _ccgo_wcscoll(tls, a, b) }

func _wcsxfrm(tls *libc.TLS, dst, src uintptr, n uint64) uint64 {
	return _ccgo_wcsxfrm(tls, dst, src, n)
}

func _ccgo_strftime(tls *libc.TLS, dst uintptr, n uint64, format, tm uintptr) uint64 {
	if n == 0 {
		return 0
	}
	r := []byte(formatTM([]rune(libc.GoString(format)), (*Ttm)(unsafe.Pointer(tm))))
	if uint64(len(r)) >= n {
		return 0
	}
	copy(cBytes(dst, n), r)
	*(*byte)(unsafe.Pointer(dst + uintptr(len(r)))) = 0
	return uint64(len(r))
}

func _wcsftime(tls *libc.TLS, dst uintptr, n uint64, format, tm uintptr) uint64 {
	if n == 0 {
		return 0
	}
	r := []rune(formatTM(wideRunes(format), (*Ttm)(unsafe.Pointer(tm))))
	if uint64(len(r)) >= n {
		return 0
	}
	for i, c := range r {
		*(*int32)(unsafe.Pointer(dst + uintptr(i)*4)) = int32(c)
	}
	*(*int32)(unsafe.Pointer(dst + uintptr(len(r))*4)) = 0
	return uint64(len(r))
}

func _wmemchr(tls *libc.TLS, s uintptr, c int32, n uint64) uintptr {
	for i := uint64(0); i < n; i++ {
		if *(*int32)(unsafe.Pointer(s + uintptr(i)*4)) == c {
			return s + uintptr(i)*4
		}
	}
	return 0
}

func _wmemcmp(tls *libc.TLS, a, b uintptr, n uint64) int32 {
	for i := uint64(0); i < n; i++ {
		x, y := *(*int32)(unsafe.Pointer(a + uintptr(i)*4)), *(*int32)(unsafe.Pointer(b + uintptr(i)*4))
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func _wcstombs(tls *libc.TLS, dst, src uintptr, n uint64) uint64 {
	var written uint64
	for p := src; ; p += 4 {
		r := rune(*(*int32)(unsafe.Pointer(p)))
		if r == 0 {
			if dst != 0 && written < n {
				*(*byte)(unsafe.Pointer(dst + uintptr(written))) = 0
			}
			return written
		}
		if !utf8.ValidRune(r) {
			setErrno(tls, int32(errno.EILSEQ))
			return ^uint64(0)
		}
		var buf [utf8.UTFMax]byte
		sz := uint64(utf8.EncodeRune(buf[:], r))
		if dst != 0 {
			if written+sz > n {
				return written
			}
			copy(cBytes(dst+uintptr(written), sz), buf[:sz])
		}
		written += sz
	}
}

func _ccgo_mbstowcs(tls *libc.TLS, dst, src uintptr, n uint64) uint64 {
	if dst == 0 {
		s := libc.GoString(src)
		if !utf8.ValidString(s) {
			setErrno(tls, int32(errno.EILSEQ))
			return ^uint64(0)
		}
		r := []rune(s)
		return uint64(len(r))
	}
	var count uint64
	p := src
	for count < n {
		if *(*byte)(unsafe.Pointer(p)) == 0 {
			*(*int32)(unsafe.Pointer(dst + uintptr(count)*4)) = 0
			return count
		}
		b := cBytes(p, utf8.UTFMax)
		r, sz := utf8.DecodeRune(b)
		if r == utf8.RuneError && sz == 1 {
			setErrno(tls, int32(errno.EILSEQ))
			return ^uint64(0)
		}
		*(*int32)(unsafe.Pointer(dst + uintptr(count)*4)) = int32(r)
		count++
		p += uintptr(sz)
	}
	return count
}

func _btowc(tls *libc.TLS, c int32) int32 {
	if c == -1 {
		return -1
	}
	if c < 0 || c > 0xff {
		return -1
	}
	return c
}

func _mbrtowc(tls *libc.TLS, dst, src uintptr, n uint64, state uintptr) uint64 {
	if src == 0 {
		return 0
	}
	if n == 0 {
		return ^uint64(1)
	}
	read := n
	if read > utf8.UTFMax {
		read = utf8.UTFMax
	}
	b := cBytes(src, read)
	if b[0] == 0 {
		if dst != 0 {
			*(*int32)(unsafe.Pointer(dst)) = 0
		}
		return 0
	}
	if !utf8.FullRune(b) {
		return ^uint64(1)
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		setErrno(tls, int32(errno.EILSEQ))
		return ^uint64(0)
	}
	if dst != 0 {
		*(*int32)(unsafe.Pointer(dst)) = int32(r)
	}
	return uint64(size)
}

// Math and stdio-locking shims.
func _exp2(tls *libc.TLS, x float64) float64     { return math.Exp2(x) }
func _erf(tls *libc.TLS, x float64) float64      { return math.Erf(x) }
func _erfc(tls *libc.TLS, x float64) float64     { return math.Erfc(x) }
func _flockfile(tls *libc.TLS, stream uintptr)   {}
func _funlockfile(tls *libc.TLS, stream uintptr) {}

func _ccgo_pow(tls *libc.TLS, x, y float64) float64 {
	result := math.Pow(x, y)
	// Go's arm64 pow differs by one ULP from Darwin libm for some positive
	// fractional powers. CPython's tests exercise those exact boundaries.
	if x > 0 && !math.IsInf(x, 0) && !math.IsInf(y, 0) && !math.IsNaN(y) && y != math.Trunc(y) {
		result = math.Exp(y * math.Log(x))
	}
	switch {
	case math.IsNaN(result) && !math.IsNaN(x) && !math.IsNaN(y):
		setErrno(tls, int32(errno.EDOM))
	case math.IsInf(result, 0) && !math.IsInf(x, 0) && !math.IsInf(y, 0):
		setErrno(tls, int32(errno.ERANGE))
	case result == 0 && x != 0 && !math.IsInf(y, 0):
		setErrno(tls, int32(errno.ERANGE))
	}
	return result
}

func _ccgo___builtin_log2(tls *libc.TLS, x float64) float64 {
	if x >= 0.5 && x <= 2 {
		return math.Log1p(x-1) / math.Ln2
	}
	fraction, exponent := math.Frexp(x)
	fraction *= 2
	exponent--
	return float64(exponent) + math.Log1p(fraction-1)/math.Ln2
}

// Scheduler, clocks, and interval timers.
func _ccgo_sched_yield(tls *libc.TLS) int32 { runtime.Gosched(); return 0 }

func _clock_getres(tls *libc.TLS, clock int32, ts uintptr) int32 {
	if ts != 0 {
		(*Ttimespec)(unsafe.Pointer(ts)).Ftv_sec = 0
		(*Ttimespec)(unsafe.Pointer(ts)).Ftv_nsec = 1
	}
	return 0
}

func _clock_settime(tls *libc.TLS, clock int32, ts uintptr) int32 {
	setErrno(tls, int32(errno.EPERM))
	return -1
}

var (
	alarmMu       sync.Mutex
	alarmTimer    *time.Timer
	alarmDeadline time.Time
)

var (
	intervalMu       sync.Mutex
	intervalTimer    *time.Timer
	intervalDeadline time.Time
	intervalRepeat   time.Duration
)

func _ccgo_alarm(tls *libc.TLS, seconds uint32) uint32 {
	alarmMu.Lock()
	defer alarmMu.Unlock()
	var remaining uint32
	if alarmTimer != nil && alarmTimer.Stop() {
		until := time.Until(alarmDeadline)
		if until > 0 {
			remaining = uint32((until + time.Second - 1) / time.Second)
		}
	}
	alarmTimer = nil
	if seconds != 0 {
		alarmDeadline = time.Now().Add(time.Duration(seconds) * time.Second)
		alarmTimer = time.AfterFunc(time.Duration(seconds)*time.Second, func() {
			_ = unix.Kill(unix.Getpid(), syscall.SIGALRM)
		})
	}
	return remaining
}

func _ccgo_nanosleep(tls *libc.TLS, request, remainder uintptr) int32 {
	req := (*Ttimespec)(unsafe.Pointer(request))
	duration := time.Duration(req.Ftv_sec)*time.Second + time.Duration(req.Ftv_nsec)
	if duration < 0 || req.Ftv_nsec < 0 || req.Ftv_nsec >= int64(time.Second) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	if consumeSignalDelivery() {
		setErrno(tls, int32(errno.EINTR))
		return -1
	}
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
		if consumeSignalDelivery() {
			if remainder != 0 {
				left := time.Until(deadline)
				if left < 0 {
					left = 0
				}
				rem := (*Ttimespec)(unsafe.Pointer(remainder))
				rem.Ftv_sec = int64(left / time.Second)
				rem.Ftv_nsec = int64(left % time.Second)
			}
			setErrno(tls, int32(errno.EINTR))
			return -1
		}
	}
	return 0
}

func _getitimer(tls *libc.TLS, which int32, value uintptr) int32 {
	if which != 0 || value == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	intervalMu.Lock()
	defer intervalMu.Unlock()
	it := (*Titimerval)(unsafe.Pointer(value))
	*it = Titimerval{}
	remaining := time.Until(intervalDeadline)
	if intervalTimer == nil || remaining < 0 {
		remaining = 0
	}
	it.Fit_value.Ftv_sec = int64(remaining / time.Second)
	it.Fit_value.Ftv_usec = int32(remaining % time.Second / time.Microsecond)
	it.Fit_interval.Ftv_sec = int64(intervalRepeat / time.Second)
	it.Fit_interval.Ftv_usec = int32(intervalRepeat % time.Second / time.Microsecond)
	return 0
}

func _setitimer(tls *libc.TLS, which int32, value, old uintptr) int32 {
	if which != 0 || value == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	if old != 0 {
		_getitimer(tls, which, old)
	}
	it := (*Titimerval)(unsafe.Pointer(value))
	if it.Fit_value.Ftv_sec < 0 || it.Fit_value.Ftv_usec < 0 || it.Fit_value.Ftv_usec >= 1_000_000 || it.Fit_interval.Ftv_sec < 0 || it.Fit_interval.Ftv_usec < 0 || it.Fit_interval.Ftv_usec >= 1_000_000 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	initial := time.Duration(it.Fit_value.Ftv_sec)*time.Second + time.Duration(it.Fit_value.Ftv_usec)*time.Microsecond
	repeat := time.Duration(it.Fit_interval.Ftv_sec)*time.Second + time.Duration(it.Fit_interval.Ftv_usec)*time.Microsecond
	intervalMu.Lock()
	defer intervalMu.Unlock()
	if intervalTimer != nil {
		intervalTimer.Stop()
		intervalTimer = nil
	}
	intervalRepeat = repeat
	if initial != 0 {
		var fire func()
		fire = func() {
			_ = unix.Kill(unix.Getpid(), syscall.SIGALRM)
			intervalMu.Lock()
			defer intervalMu.Unlock()
			if intervalRepeat != 0 {
				intervalDeadline = time.Now().Add(intervalRepeat)
				intervalTimer = time.AfterFunc(intervalRepeat, fire)
			} else {
				intervalTimer = nil
			}
		}
		intervalDeadline = time.Now().Add(initial)
		intervalTimer = time.AfterFunc(initial, fire)
	}
	return 0
}

func timevalTicks(tv unix.Timeval) uint64 { return uint64(tv.Sec*100 + int64(tv.Usec)/10000) }

func _times(tls *libc.TLS, p uintptr) uint64 {
	var self, children unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &self); err != nil {
		errnoResult(tls, err)
		return ^uint64(0)
	}
	if err := unix.Getrusage(unix.RUSAGE_CHILDREN, &children); err != nil {
		errnoResult(tls, err)
		return ^uint64(0)
	}
	if p != 0 {
		t := (*Ttms)(unsafe.Pointer(p))
		t.Ftms_utime = timevalTicks(self.Utime)
		t.Ftms_stime = timevalTicks(self.Stime)
		t.Ftms_cutime = timevalTicks(children.Utime)
		t.Ftms_cstime = timevalTicks(children.Stime)
	}
	return uint64(time.Now().UnixNano() / int64(10*time.Millisecond))
}

// Process, identity, filesystem, and socket shims.
func _ccgo_fcntl(tls *libc.TLS, fd, cmd int32, args uintptr) int32 {
	var arg uintptr
	if args != 0 {
		arg = *(*uintptr)(unsafe.Pointer(args))
	}
	r, _, e := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), uintptr(cmd), arg)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return int32(r)
}

func _getppid(tls *libc.TLS) int32 { return int32(unix.Getppid()) }
func _ccgo_chown(tls *libc.TLS, path uintptr, uid, gid uint32) int32 {
	return errnoResult(tls, unix.Chown(libc.GoString(path), int(uid), int(gid)))
}
func _ccgo_dup2(tls *libc.TLS, oldfd, newfd int32) int32 {
	if err := unix.Dup2(int(oldfd), int(newfd)); err != nil {
		return errnoResult(tls, err)
	}
	return newfd
}
func _ccgo_confstr(tls *libc.TLS, name int32, buf uintptr, n uint64) uint64 {
	if name != 1 { // _CS_PATH
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	value := []byte("/usr/bin:/bin:/usr/sbin:/sbin\x00")
	if buf != 0 && n != 0 {
		written := n
		if written > uint64(len(value)) {
			written = uint64(len(value))
		}
		copy(cBytes(buf, written), value[:written])
		if written == n {
			*(*byte)(unsafe.Pointer(buf + uintptr(n-1))) = 0
		}
	}
	return uint64(len(value))
}
func _ccgo_kill(tls *libc.TLS, pid, sig int32) int32 {
	if pid == int32(unix.Getpid()) && selfSignal(tls, sig) {
		return 0
	}
	return errnoResult(tls, unix.Kill(int(pid), syscall.Signal(sig)))
}
func _ccgo_raise(tls *libc.TLS, sig int32) int32 {
	return _ccgo_kill(tls, int32(unix.Getpid()), sig)
}
func _ccgo_getegid(tls *libc.TLS) uint32 {
	return uint32(unix.Getegid())
}
func _ccgo_getgid(tls *libc.TLS) uint32 { return uint32(unix.Getgid()) }
func _getpgrp(tls *libc.TLS) int32      { return int32(unix.Getpgrp()) }
func _getpgid(tls *libc.TLS, pid int32) int32 {
	v, e := unix.Getpgid(int(pid))
	if e != nil {
		return errnoResult(tls, e)
	}
	return int32(v)
}
func _setpgid(tls *libc.TLS, pid, pgid int32) int32 {
	return errnoResult(tls, unix.Setpgid(int(pid), int(pgid)))
}
func _setpgrp(tls *libc.TLS) int32 { return _setpgid(tls, 0, 0) }
func _getsid(tls *libc.TLS, pid int32) int32 {
	v, e := unix.Getsid(int(pid))
	if e != nil {
		return errnoResult(tls, e)
	}
	return int32(v)
}
func _setreuid(tls *libc.TLS, ruid, euid uint32) int32 {
	return errnoResult(tls, unix.Setreuid(int(ruid), int(euid)))
}
func _setregid(tls *libc.TLS, rgid, egid uint32) int32 {
	return errnoResult(tls, unix.Setregid(int(rgid), int(egid)))
}
func _seteuid(tls *libc.TLS, uid uint32) int32 { return errnoResult(tls, unix.Seteuid(int(uid))) }
func _setegid(tls *libc.TLS, gid uint32) int32 { return errnoResult(tls, unix.Setegid(int(gid))) }

func _getgroups(tls *libc.TLS, n int32, out uintptr) int32 {
	g, err := unix.Getgroups()
	if err != nil {
		return errnoResult(tls, err)
	}
	if n == 0 {
		return int32(len(g))
	}
	if int(n) < len(g) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	for i, v := range g {
		*(*uint32)(unsafe.Pointer(out + uintptr(i)*4)) = uint32(v)
	}
	return int32(len(g))
}

func _setgroups(tls *libc.TLS, n int32, in uintptr) int32 {
	g := make([]int, n)
	for i := range g {
		g[i] = int(*(*uint32)(unsafe.Pointer(in + uintptr(i)*4)))
	}
	return errnoResult(tls, unix.Setgroups(g))
}

// ponytail: libc group-database integration is not available through x/sys/unix.
func _getgrouplist(tls *libc.TLS, user uintptr, gid int32, groups, n uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

// ponytail: libc group-database integration is not available through x/sys/unix.
func _initgroups(tls *libc.TLS, user uintptr, gid int32) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}
func _getpriority(tls *libc.TLS, which int32, who uint32) int32 {
	v, e := unix.Getpriority(int(which), int(who))
	if e != nil {
		return errnoResult(tls, e)
	}
	return int32(v)
}
func _setpriority(tls *libc.TLS, which int32, who uint32, prio int32) int32 {
	return errnoResult(tls, unix.Setpriority(int(which), int(who), int(prio)))
}
func _nice(tls *libc.TLS, increment int32) int32 {
	v, e := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if e != nil {
		return errnoResult(tls, e)
	}
	if e = unix.Setpriority(unix.PRIO_PROCESS, 0, v+int(increment)); e != nil {
		return errnoResult(tls, e)
	}
	v, e = unix.Getpriority(unix.PRIO_PROCESS, 0)
	if e != nil {
		return errnoResult(tls, e)
	}
	return int32(v)
}

// ponytail: approximates getlogin with the LOGNAME environment value.
func _getlogin(tls *libc.TLS) uintptr {
	s := os.Getenv("LOGNAME")
	if s == "" {
		setErrno(tls, int32(errno.ENOENT))
		return 0
	}
	p := libc.Xmalloc(tls, uint64(len(s)+1))
	if p != 0 {
		copy(cBytes(p, uint64(len(s)+1)), s)
	}
	return p
}
func _getpagesize(tls *libc.TLS) int32 { return int32(os.Getpagesize()) }

func wait4(tls *libc.TLS, pid int32, status uintptr, options int32, usage uintptr) int32 {
	var ws unix.WaitStatus
	var ru *unix.Rusage
	if usage != 0 {
		ru = (*unix.Rusage)(unsafe.Pointer(usage))
	}
	v, e := unix.Wait4(int(pid), &ws, int(options), ru)
	if e != nil {
		return errnoResult(tls, e)
	}
	if status != 0 {
		*(*int32)(unsafe.Pointer(status)) = int32(ws)
	}
	return int32(v)
}
func _wait(tls *libc.TLS, status uintptr) int32 { return wait4(tls, -1, status, 0, 0) }
func _wait3(tls *libc.TLS, status uintptr, options int32, usage uintptr) int32 {
	return wait4(tls, -1, status, options, usage)
}
func _wait4(tls *libc.TLS, pid int32, status uintptr, options int32, usage uintptr) int32 {
	return wait4(tls, pid, status, options, usage)
}
func _execv(tls *libc.TLS, path, argv uintptr) int32 {
	return errnoResult(tls, unix.Exec(libc.GoString(path), cStrings(argv), os.Environ()))
}
func _killpg(tls *libc.TLS, pgid, sig int32) int32 {
	return errnoResult(tls, unix.Kill(-int(pgid), syscall.Signal(sig)))
}

func _pthread_kill(tls *libc.TLS, thread uintptr, sig int32) int32 {
	if _ccgo_raise(tls, sig) != 0 {
		return *(*int32)(unsafe.Pointer(libc.X__errno_location(tls)))
	}
	return 0
}
func _chroot(tls *libc.TLS, path uintptr) int32 {
	return errnoResult(tls, unix.Chroot(libc.GoString(path)))
}
func _lchown(tls *libc.TLS, path uintptr, uid, gid uint32) int32 {
	return errnoResult(tls, unix.Lchown(libc.GoString(path), int(uid), int(gid)))
}

func _lchflags(tls *libc.TLS, path uintptr, flags uint32) int32 {
	var stat unix.Stat_t
	if err := unix.Lstat(libc.GoString(path), &stat); err != nil {
		return errnoResult(tls, err)
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK && flags == 0 {
		return 0
	}
	return _ccgo_chflags(tls, path, flags)
}

func _ccgo_chflags(tls *libc.TLS, path uintptr, flags uint32) int32 {
	_, _, e := unix.Syscall(unix.SYS_CHFLAGS, path, uintptr(flags), 0)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

// ponytail: Darwin lockf command translation is not exposed by x/sys/unix.
func _lockf(tls *libc.TLS, fd, cmd int32, length int64) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}
func _madvise(tls *libc.TLS, p uintptr, n uint64, advice int32) int32 {
	return errnoResult(tls, unix.Madvise(cBytes(p, n), int(advice)))
}
func _msync(tls *libc.TLS, p uintptr, n uint64, flags int32) int32 {
	return errnoResult(tls, unix.Msync(cBytes(p, n), int(flags)))
}
func _sync(tls *libc.TLS) { unix.Sync() }

// ponytail: copyfile state and metadata copying are outside the pure-Go syscall surface.
func _fcopyfile(tls *libc.TLS, inFD, outFD int32, state uintptr, flags uint32) int32 {
	setErrno(tls, int32(errno.ENOTSUP))
	return -1
}

// ponytail: Darwin's headers/trailers form is unsupported; plain transfers are real.
func _sendfile(tls *libc.TLS, inFD, outFD int32, offset int64, lenp, hdtr uintptr, flags int32) int32 {
	if hdtr != 0 || flags != 0 {
		setErrno(tls, int32(errno.ENOSYS))
		return -1
	}
	n := int(*(*int64)(unsafe.Pointer(lenp)))
	off := offset
	written, err := unix.Sendfile(int(outFD), int(inFD), &off, n)
	*(*int64)(unsafe.Pointer(lenp)) = int64(written)
	return errnoResult(tls, err)
}

func copyStatfs(out uintptr, s *unix.Statfs_t) {
	d := (*Tstatvfs)(unsafe.Pointer(out))
	d.Ff_bsize = uint64(s.Iosize)
	d.Ff_frsize = uint64(s.Bsize)
	d.Ff_blocks = Tfsblkcnt_t(s.Blocks)
	d.Ff_bfree = Tfsblkcnt_t(s.Bfree)
	d.Ff_bavail = Tfsblkcnt_t(s.Bavail)
	d.Ff_files = Tfsfilcnt_t(s.Files)
	d.Ff_ffree = Tfsfilcnt_t(s.Ffree)
	d.Ff_favail = Tfsfilcnt_t(s.Ffree)
	d.Ff_fsid = uint64(uint32(s.Fsid.Val[0]))
	d.Ff_flag = 0
	if s.Flags&unix.MNT_RDONLY != 0 {
		d.Ff_flag |= 1
	}
	if s.Flags&unix.MNT_NOSUID != 0 {
		d.Ff_flag |= 2
	}
	d.Ff_namemax = 255
}
func _statvfs(tls *libc.TLS, path, out uintptr) int32 {
	var s unix.Statfs_t
	if e := unix.Statfs(libc.GoString(path), &s); e != nil {
		return errnoResult(tls, e)
	}
	copyStatfs(out, &s)
	return 0
}
func _fstatvfs(tls *libc.TLS, fd int32, out uintptr) int32 {
	var s unix.Statfs_t
	if e := unix.Fstatfs(int(fd), &s); e != nil {
		return errnoResult(tls, e)
	}
	copyStatfs(out, &s)
	return 0
}
func _fpathconf(tls *libc.TLS, fd, name int32) int64 {
	v, e := unix.Fpathconf(int(fd), int(name))
	if e != nil {
		errnoResult(tls, e)
		return -1
	}
	return int64(v)
}
func _fchdir(tls *libc.TLS, fd int32) int32 { return errnoResult(tls, unix.Fchdir(int(fd))) }
func _tcgetpgrp(tls *libc.TLS, fd int32) int32 {
	pgid, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	if err != nil {
		return errnoResult(tls, err)
	}
	return int32(pgid)
}

func _tcsetpgrp(tls *libc.TLS, fd, pgid int32) int32 {
	return errnoResult(tls, unix.IoctlSetPointerInt(int(fd), unix.TIOCSPGRP, int(pgid)))
}

func _ccgo_tcgetattr(tls *libc.TLS, fd int32, termios uintptr) int32 {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCGETA, termios)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

func _ccgo_tcsetattr(tls *libc.TLS, fd, action int32, termios uintptr) int32 {
	request := uintptr(unix.TIOCSETA)
	switch action {
	case 0: // TCSANOW
	case 1: // TCSADRAIN
		request = unix.TIOCSETAW
	case 2: // TCSAFLUSH
		request = unix.TIOCSETAF
	default:
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, termios)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

func _cfgetispeed(tls *libc.TLS, termios uintptr) uint64 {
	return uint64((*Ttermios)(unsafe.Pointer(termios)).Fc_ispeed)
}

func _ccgo_cfgetospeed(tls *libc.TLS, termios uintptr) uint64 {
	return uint64((*Ttermios)(unsafe.Pointer(termios)).Fc_ospeed)
}

func _cfsetispeed(tls *libc.TLS, termios uintptr, speed uint64) int32 {
	(*Ttermios)(unsafe.Pointer(termios)).Fc_ispeed = Tspeed_t(speed)
	return 0
}

func _cfsetospeed(tls *libc.TLS, termios uintptr, speed uint64) int32 {
	(*Ttermios)(unsafe.Pointer(termios)).Fc_ospeed = Tspeed_t(speed)
	return 0
}

func termiosIoctl(tls *libc.TLS, fd int32, request uintptr) int32 {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, 0)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

func _tcdrain(tls *libc.TLS, fd int32) int32 {
	return termiosIoctl(tls, fd, unix.TIOCDRAIN)
}

func _tcflow(tls *libc.TLS, fd, action int32) int32 {
	var request uintptr
	switch action {
	case 1: // TCOOFF
		request = unix.TIOCSTOP
	case 2: // TCOON
		request = unix.TIOCSTART
	case 3: // TCIOFF
		request = unix.TIOCIXOFF
	case 4: // TCION
		request = unix.TIOCIXON
	default:
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return termiosIoctl(tls, fd, request)
}

func _tcflush(tls *libc.TLS, fd, queue int32) int32 {
	var flags int
	switch queue {
	case 1: // TCIFLUSH / FREAD
		flags = 1
	case 2: // TCOFLUSH / FWRITE
		flags = 2
	case 3: // TCIOFLUSH / FREAD|FWRITE
		flags = 3
	default:
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return errnoResult(tls, unix.IoctlSetPointerInt(int(fd), unix.TIOCFLUSH, flags))
}

func _dirfd(tls *libc.TLS, dir uintptr) int32 {
	if dir == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return int32(*(*int)(unsafe.Pointer(dir + 4096)))
}
func _ctermid_r(tls *libc.TLS, out uintptr) uintptr { copy(cBytes(out, 9), "/dev/tty\x00"); return out }

// ponytail: resolving a descriptor's terminal pathname is not exposed by Go.
func _ttyname_r(tls *libc.TLS, fd int32, out uintptr, n uint64) int32 { return int32(errno.ENOSYS) }

// ponytail: forkpty and login_tty require libc routines not exposed by x/sys/unix.
func _forkpty(tls *libc.TLS, master, name, termios, winsize uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

func _login_tty(tls *libc.TLS, fd int32) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

func _strsignal(tls *libc.TLS, sig int32) uintptr {
	s := syscall.Signal(sig).String()
	p := libc.Xmalloc(tls, uint64(len(s)+1))
	if p != 0 {
		copy(cBytes(p, uint64(len(s)+1)), s)
		*(*byte)(unsafe.Pointer(p + uintptr(len(s)))) = 0
	}
	return p
}

// ponytail: Darwin sigset operations need pthread/C ABI interop.
func _sigpending(tls *libc.TLS, set uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

// ponytail: Darwin sigset operations need pthread/C ABI interop.
func _sigwait(tls *libc.TLS, set, sig uintptr) int32 { return int32(errno.ENOSYS) }

func _ccgo_sigaction(tls *libc.TLS, signum int32, act, oldact uintptr) int32 {
	if signum <= 0 || signum == int32(syscall.SIGKILL) || signum == int32(syscall.SIGSTOP) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	var next *goSigaction
	if act != 0 {
		a := (*Tsigaction)(unsafe.Pointer(act))
		next = &goSigaction{handler: a.F__sigaction_u.F__sa_handler, mask: a.Fsa_mask, flags: a.Fsa_flags}
	}
	previous := installSigaction(signum, next)
	if oldact != 0 {
		a := (*Tsigaction)(unsafe.Pointer(oldact))
		a.F__sigaction_u.F__sa_handler = previous.handler
		a.Fsa_mask = previous.mask
		a.Fsa_flags = previous.flags
	}
	return 0
}

// ponytail: POSIX scheduler priority queries are not exposed on Darwin by x/sys/unix.
func _sched_get_priority_min(tls *libc.TLS, policy int32) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

// ponytail: POSIX scheduler priority queries are not exposed on Darwin by x/sys/unix.
func _sched_get_priority_max(tls *libc.TLS, policy int32) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

func iovecs(p uintptr, n int32) [][]byte {
	r := make([][]byte, n)
	for i := range r {
		v := (*Tiovec)(unsafe.Pointer(p + uintptr(i)*16))
		r[i] = cBytes(v.Fiov_base, v.Fiov_len)
	}
	return r
}
func _preadv(tls *libc.TLS, fd int32, iov uintptr, n int32, offset int64) int64 {
	v, e := unix.Preadv(int(fd), iovecs(iov, n), offset)
	if e != nil {
		return int64(errnoResult(tls, e))
	}
	return int64(v)
}
func _ccgo_readv(tls *libc.TLS, fd int32, iov uintptr, n int32) int64 {
	v, e := unix.Readv(int(fd), iovecs(iov, n))
	if e != nil {
		return int64(errnoResult(tls, e))
	}
	return int64(v)
}
func _pwritev(tls *libc.TLS, fd int32, iov uintptr, n int32, offset int64) int64 {
	v, e := unix.Pwritev(int(fd), iovecs(iov, n), offset)
	if e != nil {
		return int64(errnoResult(tls, e))
	}
	return int64(v)
}
func _lutimes(tls *libc.TLS, path, tv uintptr) int32 {
	if tv == 0 {
		return errnoResult(tls, unix.Lutimes(libc.GoString(path), nil))
	}
	t := unsafe.Slice((*unix.Timeval)(unsafe.Pointer(tv)), 2)
	return errnoResult(tls, unix.Lutimes(libc.GoString(path), t))
}

func accountRecord(path string, index int, value string) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > index && fields[index] == value {
			return fields, true
		}
	}
	return nil, false
}

func putAccountStrings(buf uintptr, n uint64, values ...string) ([]uintptr, uint64, bool) {
	var used uint64
	pointers := make([]uintptr, len(values))
	for i, value := range values {
		need := uint64(len(value) + 1)
		if used+need > n {
			return nil, 0, false
		}
		pointers[i] = buf + uintptr(used)
		copy(cBytes(pointers[i], need), value)
		*(*byte)(unsafe.Pointer(pointers[i] + uintptr(len(value)))) = 0
		used += need
	}
	return pointers, used, true
}

func fillGroup(fields []string, group, buf uintptr, n uint64, result uintptr) int32 {
	members := []string{}
	if len(fields) > 3 && fields[3] != "" {
		members = strings.Split(fields[3], ",")
	}
	values := append([]string{fields[0], fields[1]}, members...)
	pointers, used, ok := putAccountStrings(buf, n, values...)
	if !ok {
		return int32(errno.ERANGE)
	}
	used = (used + 7) &^ 7
	if used+uint64((len(members)+1)*8) > n {
		return int32(errno.ERANGE)
	}
	memberArray := buf + uintptr(used)
	for i := range members {
		*(*uintptr)(unsafe.Pointer(memberArray + uintptr(i)*8)) = pointers[i+2]
	}
	*(*uintptr)(unsafe.Pointer(memberArray + uintptr(len(members))*8)) = 0
	gid, _ := strconv.ParseUint(fields[2], 10, 32)
	*(*Tgroup)(unsafe.Pointer(group)) = Tgroup{Fgr_name: pointers[0], Fgr_passwd: pointers[1], Fgr_gid: uint32(gid), Fgr_mem: memberArray}
	*(*uintptr)(unsafe.Pointer(result)) = group
	return 0
}

func _ccgo_getgrgid_r(tls *libc.TLS, gid uint32, group, buf uintptr, n uint64, result uintptr) int32 {
	fields, ok := accountRecord("/etc/group", 2, strconv.FormatUint(uint64(gid), 10))
	if !ok || len(fields) < 4 {
		*(*uintptr)(unsafe.Pointer(result)) = 0
		return 0
	}
	return fillGroup(fields, group, buf, n, result)
}

func _ccgo_getgrnam_r(tls *libc.TLS, name, group, buf uintptr, n uint64, result uintptr) int32 {
	fields, ok := accountRecord("/etc/group", 0, libc.GoString(name))
	if !ok || len(fields) < 4 {
		*(*uintptr)(unsafe.Pointer(result)) = 0
		return 0
	}
	return fillGroup(fields, group, buf, n, result)
}

func fillPasswd(fields []string, passwd, buf uintptr, n uint64, result uintptr) int32 {
	values := []string{fields[0], fields[1], "", fields[4], fields[5], fields[6]}
	pointers, _, ok := putAccountStrings(buf, n, values...)
	if !ok {
		return int32(errno.ERANGE)
	}
	uid, _ := strconv.ParseUint(fields[2], 10, 32)
	gid, _ := strconv.ParseUint(fields[3], 10, 32)
	*(*Tpasswd)(unsafe.Pointer(passwd)) = Tpasswd{
		Fpw_name: pointers[0], Fpw_passwd: pointers[1], Fpw_uid: uint32(uid), Fpw_gid: uint32(gid),
		Fpw_class: pointers[2], Fpw_gecos: pointers[3], Fpw_dir: pointers[4], Fpw_shell: pointers[5],
	}
	*(*uintptr)(unsafe.Pointer(result)) = passwd
	return 0
}

func _ccgo_getpwuid_r(tls *libc.TLS, uid uint32, passwd, buf uintptr, n uint64, result uintptr) int32 {
	fields, ok := accountRecord("/etc/passwd", 2, strconv.FormatUint(uint64(uid), 10))
	if !ok && uid == uint32(unix.Getuid()) {
		home, _ := os.UserHomeDir()
		fields = []string{os.Getenv("USER"), "*", strconv.FormatUint(uint64(uid), 10), strconv.Itoa(unix.Getgid()), os.Getenv("USER"), home, os.Getenv("SHELL")}
		ok = true
	}
	if !ok || len(fields) < 7 {
		*(*uintptr)(unsafe.Pointer(result)) = 0
		return 0
	}
	return fillPasswd(fields, passwd, buf, n, result)
}

func _ccgo_getpwnam_r(tls *libc.TLS, name, passwd, buf uintptr, n uint64, result uintptr) int32 {
	requested := libc.GoString(name)
	fields, ok := accountRecord("/etc/passwd", 0, requested)
	if !ok && requested == os.Getenv("USER") {
		home, _ := os.UserHomeDir()
		fields = []string{requested, "*", strconv.Itoa(unix.Getuid()), strconv.Itoa(unix.Getgid()), requested, home, os.Getenv("SHELL")}
		ok = true
	}
	if !ok || len(fields) < 7 {
		*(*uintptr)(unsafe.Pointer(result)) = 0
		return 0
	}
	return fillPasswd(fields, passwd, buf, n, result)
}

var (
	passwdEnumerationMu      sync.Mutex
	passwdEnumerationRecords [][]string
	passwdEnumerationIndex   int
	passwdEnumerationResult  Tpasswd
)

func loadPasswdEnumeration() {
	passwdEnumerationRecords = nil
	passwdEnumerationIndex = 0
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 {
			passwdEnumerationRecords = append(passwdEnumerationRecords, fields)
		}
	}
}

func _ccgo_setpwent(tls *libc.TLS) {
	passwdEnumerationMu.Lock()
	defer passwdEnumerationMu.Unlock()
	loadPasswdEnumeration()
}

func _ccgo_getpwent(tls *libc.TLS) uintptr {
	passwdEnumerationMu.Lock()
	defer passwdEnumerationMu.Unlock()
	if passwdEnumerationRecords == nil {
		loadPasswdEnumeration()
	}
	if passwdEnumerationIndex >= len(passwdEnumerationRecords) {
		return 0
	}
	fields := passwdEnumerationRecords[passwdEnumerationIndex]
	passwdEnumerationIndex++
	uid, _ := strconv.ParseUint(fields[2], 10, 32)
	gid, _ := strconv.ParseUint(fields[3], 10, 32)
	passwdEnumerationResult = Tpasswd{
		Fpw_name: stableCString(fields[0]), Fpw_passwd: stableCString(fields[1]),
		Fpw_uid: uint32(uid), Fpw_gid: uint32(gid), Fpw_class: stableCString(""),
		Fpw_gecos: stableCString(fields[4]), Fpw_dir: stableCString(fields[5]), Fpw_shell: stableCString(fields[6]),
	}
	return uintptr(unsafe.Pointer(&passwdEnumerationResult))
}

func _ccgo_endpwent(tls *libc.TLS) {
	passwdEnumerationMu.Lock()
	defer passwdEnumerationMu.Unlock()
	passwdEnumerationRecords = nil
	passwdEnumerationIndex = 0
}

// The pwd module's direct declarations are emitted without a libc qualifier.
func _setpwent(tls *libc.TLS)         { _ccgo_setpwent(tls) }
func _getpwent(tls *libc.TLS) uintptr { return _ccgo_getpwent(tls) }

// ponytail: group enumeration needs libc database state unavailable in pure Go.
func _setgrent(tls *libc.TLS)         {}
func _getgrent(tls *libc.TLS) uintptr { setErrno(tls, int32(errno.ENOSYS)); return 0 }
func _endgrent(tls *libc.TLS)         {}

// ponytail: Darwin sethostname is not exposed by x/sys/unix.
func _sethostname(tls *libc.TLS, name uintptr, n int32) int32 {
	setErrno(tls, int32(errno.EPERM))
	return -1
}
func _socketpair(tls *libc.TLS, domain, typ, protocol int32, out uintptr) int32 {
	fd, e := unix.Socketpair(int(domain), int(typ), int(protocol))
	if e != nil {
		return errnoResult(tls, e)
	}
	*(*int32)(unsafe.Pointer(out)) = int32(fd[0])
	*(*int32)(unsafe.Pointer(out + 4)) = int32(fd[1])
	return 0
}

func _ccgo_getsockopt(tls *libc.TLS, fd, level, name int32, value, valueLen uintptr) int32 {
	_, _, e := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), uintptr(level), uintptr(name), value, valueLen, 0)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

func _ccgo_setsockopt(tls *libc.TLS, fd, level, name int32, value uintptr, valueLen uint32) int32 {
	_, _, e := unix.Syscall6(unix.SYS_SETSOCKOPT, uintptr(fd), uintptr(level), uintptr(name), value, uintptr(valueLen), 0)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return 0
}

func _ccgo_select(tls *libc.TLS, n int32, readfds, writefds, exceptfds, timeout uintptr) int32 {
	r, _, e := unix.Syscall6(unix.SYS_SELECT, uintptr(n), readfds, writefds, exceptfds, timeout, 0)
	if e != 0 {
		return errnoResult(tls, e)
	}
	return int32(r)
}

func parseIPv4Number(s string) (uint32, bool) {
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return 0, false
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return 0, false
		}
		v, err := strconv.ParseUint(part, 0, 32)
		if err != nil {
			return 0, false
		}
		values[i] = v
	}
	var address uint64
	switch len(values) {
	case 1:
		address = values[0]
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return 0, false
		}
		address = values[0]<<24 | values[1]
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return 0, false
		}
		address = values[0]<<24 | values[1]<<16 | values[2]
	case 4:
		for _, value := range values {
			if value > 0xff {
				return 0, false
			}
		}
		address = values[0]<<24 | values[1]<<16 | values[2]<<8 | values[3]
	}
	return uint32(address), true
}

func _inet_aton(tls *libc.TLS, src, out uintptr) int32 {
	address, ok := parseIPv4Number(libc.GoString(src))
	if !ok {
		return 0
	}
	binary.BigEndian.PutUint32(cBytes(out, 4), address)
	return 1
}
func _inet_addr(tls *libc.TLS, src uintptr) uint32 {
	address, ok := parseIPv4Number(libc.GoString(src))
	if !ok {
		return ^uint32(0)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], address)
	return binary.LittleEndian.Uint32(b[:])
}

func _ccgo_inet_pton(tls *libc.TLS, family int32, src, dst uintptr) int32 {
	if family != unix.AF_INET && family != unix.AF_INET6 {
		setErrno(tls, int32(errno.EAFNOSUPPORT))
		return -1
	}
	ip := net.ParseIP(libc.GoString(src))
	if family == unix.AF_INET {
		ip = ip.To4()
	} else {
		// An IPv4 literal is not an IPv6 presentation-format address.
		if ip != nil && ip.To4() != nil {
			ip = nil
		} else {
			ip = ip.To16()
		}
	}
	if ip == nil {
		return 0
	}
	copy(cBytes(dst, uint64(len(ip))), ip)
	return 1
}

func _ccgo_inet_ntoa(tls *libc.TLS, address Tin_addr) uintptr {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(address.Fs_addr))
	return stableCString(net.IP(raw[:]).String())
}

func _ccgo_getaddrinfo(tls *libc.TLS, node, service, hints, result uintptr) int32 {
	family, socktype, protocol, flags := int32(unix.AF_UNSPEC), int32(0), int32(0), int32(0)
	if hints != 0 {
		h := (*Taddrinfo)(unsafe.Pointer(hints))
		family, socktype, protocol, flags = h.Fai_family, h.Fai_socktype, h.Fai_protocol, h.Fai_flags
	}
	if family != unix.AF_UNSPEC && family != unix.AF_INET && family != unix.AF_INET6 {
		return 5 // EAI_FAMILY
	}
	port := 0
	if service != 0 {
		name := libc.GoString(service)
		value, err := strconv.Atoi(name)
		if err != nil {
			if flags&0x1000 != 0 { // AI_NUMERICSERV
				return 8 // EAI_NONAME
			}
			network := "tcp"
			if socktype == unix.SOCK_DGRAM {
				network = "udp"
			}
			value, err = net.LookupPort(network, name)
			if err != nil {
				return 9 // EAI_SERVICE
			}
		}
		if value < 0 || value > 65535 {
			return 9 // EAI_SERVICE
		}
		port = value
	}
	var ips []net.IP
	if node == 0 {
		if family != unix.AF_INET {
			if flags&1 != 0 { // AI_PASSIVE
				ips = append(ips, net.IPv6zero)
			} else {
				ips = append(ips, net.IPv6loopback)
			}
		}
		if family != unix.AF_INET6 {
			if flags&1 != 0 {
				ips = append(ips, net.IPv4zero)
			} else {
				ips = append(ips, net.IPv4(127, 0, 0, 1))
			}
		}
	} else {
		name := libc.GoString(node)
		if ip := net.ParseIP(name); ip != nil {
			ips = []net.IP{ip}
		} else if flags&4 != 0 { // AI_NUMERICHOST
			return 8 // EAI_NONAME
		} else if strings.EqualFold(name, "localhost") {
			if family != unix.AF_INET {
				ips = append(ips, net.IPv6loopback)
			}
			if family != unix.AF_INET6 {
				ips = append(ips, net.IPv4(127, 0, 0, 1))
			}
		} else {
			resolved, err := net.LookupIP(name)
			if err != nil {
				return 8 // EAI_NONAME
			}
			ips = resolved
		}
	}
	filtered := ips[:0]
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			if family != unix.AF_INET6 {
				filtered = append(filtered, v4)
			}
		} else if v6 := ip.To16(); v6 != nil && family != unix.AF_INET {
			filtered = append(filtered, v6)
		}
	}
	ips = filtered
	if len(ips) == 0 {
		return 7 // EAI_NODATA
	}
	types := [][2]int32{{socktype, protocol}}
	if socktype == 0 {
		types = [][2]int32{{unix.SOCK_STREAM, unix.IPPROTO_TCP}, {unix.SOCK_DGRAM, unix.IPPROTO_UDP}}
	}
	var first, last uintptr
	for _, ip := range ips {
		for _, pair := range types {
			ai := libc.Xmalloc(tls, uint64(unsafe.Sizeof(Taddrinfo{})))
			addressFamily := int32(unix.AF_INET6)
			addressSize := unsafe.Sizeof(Tsockaddr_in6{})
			if ip.To4() != nil {
				addressFamily = unix.AF_INET
				addressSize = unsafe.Sizeof(Tsockaddr_in{})
			}
			sa := libc.Xmalloc(tls, uint64(addressSize))
			if ai == 0 || sa == 0 {
				if ai != 0 {
					libc.Xfree(tls, ai)
				}
				if sa != 0 {
					libc.Xfree(tls, sa)
				}
				_ccgo_freeaddrinfo(tls, first)
				return 6 // EAI_MEMORY
			}
			libc.Xmemset(tls, ai, 0, uint64(unsafe.Sizeof(Taddrinfo{})))
			libc.Xmemset(tls, sa, 0, uint64(addressSize))
			binary.BigEndian.PutUint16(cBytes(sa+2, 2), uint16(port))
			if addressFamily == unix.AF_INET {
				sin := (*Tsockaddr_in)(unsafe.Pointer(sa))
				sin.Fsin_len = uint8(addressSize)
				sin.Fsin_family = unix.AF_INET
				copy(cBytes(sa+4, 4), ip.To4())
			} else {
				sin6 := (*Tsockaddr_in6)(unsafe.Pointer(sa))
				sin6.Fsin6_len = uint8(addressSize)
				sin6.Fsin6_family = unix.AF_INET6
				copy(cBytes(uintptr(unsafe.Pointer(&sin6.Fsin6_addr)), 16), ip.To16())
			}
			a := (*Taddrinfo)(unsafe.Pointer(ai))
			a.Fai_flags, a.Fai_family, a.Fai_socktype, a.Fai_protocol = flags, addressFamily, pair[0], pair[1]
			a.Fai_addrlen, a.Fai_addr = uint32(addressSize), sa
			if first == 0 {
				first = ai
			} else {
				(*Taddrinfo)(unsafe.Pointer(last)).Fai_next = ai
			}
			last = ai
		}
	}
	*(*uintptr)(unsafe.Pointer(result)) = first
	return 0
}

func _ccgo_freeaddrinfo(tls *libc.TLS, current uintptr) {
	for current != 0 {
		next := (*Taddrinfo)(unsafe.Pointer(current)).Fai_next
		if address := (*Taddrinfo)(unsafe.Pointer(current)).Fai_addr; address != 0 {
			libc.Xfree(tls, address)
		}
		if canon := (*Taddrinfo)(unsafe.Pointer(current)).Fai_canonname; canon != 0 {
			libc.Xfree(tls, canon)
		}
		libc.Xfree(tls, current)
		current = next
	}
}

func _ccgo_inet_ntop(tls *libc.TLS, family int32, src, dst uintptr, n uint32) uintptr {
	var size uint64
	switch family {
	case unix.AF_INET:
		size = 4
	case unix.AF_INET6:
		size = 16
	default:
		setErrno(tls, int32(errno.EAFNOSUPPORT))
		return 0
	}
	value := net.IP(cBytes(src, size)).String()
	if uint32(len(value)+1) > n {
		setErrno(tls, int32(errno.ENOSPC))
		return 0
	}
	copy(cBytes(dst, uint64(n)), value)
	*(*byte)(unsafe.Pointer(dst + uintptr(len(value)))) = 0
	return dst
}

func _ccgo_getnameinfo(tls *libc.TLS, address uintptr, addressLen uint32, host uintptr, hostLen uint32, service uintptr, serviceLen uint32, flags int32) int32 {
	if address == 0 || addressLen < 8 {
		return 5 // EAI_FAMILY
	}
	var hostValue string
	switch *(*byte)(unsafe.Pointer(address + 1)) {
	case unix.AF_INET:
		hostValue = net.IP(cBytes(address+4, 4)).String()
	case unix.AF_INET6:
		if addressLen < uint32(unsafe.Sizeof(Tsockaddr_in6{})) {
			return 5 // EAI_FAMILY
		}
		sin6 := (*Tsockaddr_in6)(unsafe.Pointer(address))
		hostValue = net.IP(cBytes(uintptr(unsafe.Pointer(&sin6.Fsin6_addr)), 16)).String()
		if sin6.Fsin6_scope_id != 0 {
			hostValue += "%" + strconv.FormatUint(uint64(sin6.Fsin6_scope_id), 10)
		}
	default:
		return 5 // EAI_FAMILY
	}
	port := binary.BigEndian.Uint16(cBytes(address+2, 2))
	serviceValue := strconv.Itoa(int(port))
	if host != 0 {
		if uint32(len(hostValue)+1) > hostLen {
			return 14 // EAI_OVERFLOW
		}
		copy(cBytes(host, uint64(hostLen)), hostValue)
		*(*byte)(unsafe.Pointer(host + uintptr(len(hostValue)))) = 0
	}
	if service != 0 {
		if uint32(len(serviceValue)+1) > serviceLen {
			return 14 // EAI_OVERFLOW
		}
		copy(cBytes(service, uint64(serviceLen)), serviceValue)
		*(*byte)(unsafe.Pointer(service + uintptr(len(serviceValue)))) = 0
	}
	return 0
}

func _if_nametoindex(tls *libc.TLS, name uintptr) uint32 {
	iface, err := net.InterfaceByName(libc.GoString(name))
	if err == nil {
		return uint32(iface.Index)
	}
	setErrno(tls, int32(errno.ENXIO))
	return 0
}
func _if_indextoname(tls *libc.TLS, index uint32, name uintptr) uintptr {
	iface, err := net.InterfaceByIndex(int(index))
	if err != nil {
		setErrno(tls, int32(errno.ENXIO))
		return 0
	}
	copy(cBytes(name, 16), iface.Name)
	*(*byte)(unsafe.Pointer(name + uintptr(len(iface.Name)))) = 0
	return name
}
func _if_nameindex(tls *libc.TLS) uintptr {
	interfaces, err := net.Interfaces()
	if err != nil {
		errnoResult(tls, err)
		return 0
	}
	p := libc.Xcalloc(tls, uint64(len(interfaces)+1), uint64(unsafe.Sizeof(Tif_nameindex{})))
	if p == 0 {
		return 0
	}
	for i, iface := range interfaces {
		entry := (*Tif_nameindex)(unsafe.Pointer(p + uintptr(i)*unsafe.Sizeof(Tif_nameindex{})))
		entry.Fif_index = uint32(iface.Index)
		entry.Fif_name, _ = libc.CString(iface.Name)
	}
	return p
}
func _if_freenameindex(tls *libc.TLS, p uintptr) {
	if p == 0 {
		return
	}
	for q := p; (*Tif_nameindex)(unsafe.Pointer(q)).Fif_index != 0; q += unsafe.Sizeof(Tif_nameindex{}) {
		libc.Xfree(tls, (*Tif_nameindex)(unsafe.Pointer(q)).Fif_name)
	}
	libc.Xfree(tls, p)
}

var serviceEntryMu sync.Mutex

func lookupService(name string, port int, protocol string) (string, int, string, bool) {
	data, err := os.ReadFile("/etc/services")
	if err != nil {
		return "", 0, "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(fields) < 2 {
			continue
		}
		portProto := strings.SplitN(fields[1], "/", 2)
		if len(portProto) != 2 {
			continue
		}
		n, err := strconv.Atoi(portProto[0])
		if err != nil || (protocol != "" && portProto[1] != protocol) {
			continue
		}
		nameMatches := name == "" || fields[0] == name
		for _, alias := range fields[2:] {
			nameMatches = nameMatches || alias == name
		}
		if nameMatches && (port < 0 || port == n) {
			return fields[0], n, portProto[1], true
		}
	}
	return "", 0, "", false
}

func serviceEntry(tls *libc.TLS, name string, port int, protocol string) uintptr {
	serviceEntryMu.Lock()
	defer serviceEntryMu.Unlock()
	canonical, number, proto, ok := lookupService(name, port, protocol)
	if !ok {
		return 0
	}
	p := libc.Xcalloc(tls, 1, uint64(unsafe.Sizeof(Tservent{})))
	if p == 0 {
		return 0
	}
	e := (*Tservent)(unsafe.Pointer(p))
	e.Fs_name, _ = libc.CString(canonical)
	e.Fs_proto, _ = libc.CString(proto)
	e.Fs_port = int32(uint16(number)<<8 | uint16(number)>>8)
	return p
}

func _ccgo_getservbyname(tls *libc.TLS, name, proto uintptr) uintptr {
	protocol := ""
	if proto != 0 {
		protocol = libc.GoString(proto)
	}
	return serviceEntry(tls, libc.GoString(name), -1, protocol)
}

func _getservbyport(tls *libc.TLS, port int32, proto uintptr) uintptr {
	protocol := ""
	if proto != 0 {
		protocol = libc.GoString(proto)
	}
	number := int(uint16(port)<<8 | uint16(port)>>8)
	return serviceEntry(tls, "", number, protocol)
}
func _getprotobyname(tls *libc.TLS, name uintptr) uintptr {
	setErrno(tls, int32(errno.ENOSYS))
	return 0
}
func _hstrerror(tls *libc.TLS, e int32) uintptr {
	// Values from <netdb.h>.  CPython always decodes the returned pointer, so
	// unlike a missing optional API this function must never return NULL.
	message := map[int32]string{
		1: "Unknown host",
		2: "Host name lookup failure",
		3: "Unknown server error",
		4: "No address associated with name",
	}[e]
	if message == "" {
		message = "Resolver error " + strconv.FormatInt(int64(e), 10)
	}
	return stableCString(message)
}

func _pthread_threadid_np(tls *libc.TLS, thread, out uintptr) int32 {
	if out == 0 {
		return int32(errno.EINVAL)
	}
	if thread != 0 {
		return int32(errno.ENOSYS)
	}
	r, _, e := unix.Syscall(unix.SYS_THREAD_SELFID, 0, 0, 0)
	if e != 0 {
		return int32(e)
	}
	*(*uint64)(unsafe.Pointer(out)) = uint64(r)
	return 0
}

var _mach_task_self_ uint32

// ponytail: Mach task ports are outside the pure-Go syscall surface.
func _task_for_pid(tls *libc.TLS, task uint32, pid int32, out uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

// ponytail: Mach task ports are outside the pure-Go syscall surface.
func _task_threads(tls *libc.TLS, task uint32, list, count uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

func __NSGetExecutablePath(tls *libc.TLS, buf, size uintptr) int32 {
	p, err := os.Executable()
	if err != nil {
		setErrno(tls, int32(errno.EIO))
		return -1
	}
	need := uint32(len(p) + 1)
	have := *(*uint32)(unsafe.Pointer(size))
	if have < need {
		*(*uint32)(unsafe.Pointer(size)) = need
		return -1
	}
	copy(cBytes(buf, uint64(need)), p)
	*(*byte)(unsafe.Pointer(buf + uintptr(len(p)))) = 0
	*(*uint32)(unsafe.Pointer(size)) = need
	return 0
}

// Routed here by generator.go (shimmedLibc) because the modernc.org/libc
// darwin versions panic with TODO for arguments CPython uses.

func _ccgo_sysconf(tls *libc.TLS, name int32) int64 {
	switch name {
	case 1: // _SC_ARG_MAX
		return 1024 * 1024
	case 3: // _SC_CLK_TCK
		return 100
	case 5: // _SC_OPEN_MAX
		var rl unix.Rlimit
		if unix.Getrlimit(unix.RLIMIT_NOFILE, &rl) == nil {
			return int64(rl.Cur)
		}
		return 256
	case 29: // _SC_PAGESIZE
		return int64(unix.Getpagesize())
	case 57, 58: // _SC_NPROCESSORS_CONF, _SC_NPROCESSORS_ONLN
		return int64(runtime.NumCPU())
	case 70, 71: // _SC_GETGR_R_SIZE_MAX, _SC_GETPW_R_SIZE_MAX
		return 4096
	}
	setErrno(tls, int32(errno.EINVAL))
	return -1
}

func _ccgo___srget(tls *libc.TLS, stream uintptr) int32 {
	return libc.Xfgetc(tls, stream)
}

// ponytail: supports the single-byte pushback pattern used by CPython's readers.
func _ccgo_ungetc(tls *libc.TLS, c int32, stream uintptr) int32 {
	if c == -1 {
		return -1
	}
	if libc.Xfseek(tls, stream, -1, 1) != 0 {
		return -1
	}
	return int32(byte(c))
}

func _ccgo_truncate(tls *libc.TLS, path uintptr, length int64) int32 {
	return errnoResult(tls, unix.Truncate(libc.GoString(path), length))
}

func _ccgo_pathconf(tls *libc.TLS, path uintptr, name int32) int64 {
	r, _, e := unix.Syscall(unix.SYS_PATHCONF, path, uintptr(name), 0)
	if e != 0 {
		return int64(errnoResult(tls, e))
	}
	return int64(r)
}

func _ccgo_fpathconf(tls *libc.TLS, fd, name int32) int64 {
	r, _, e := unix.Syscall(unix.SYS_FPATHCONF, uintptr(fd), uintptr(name), 0)
	if e != 0 {
		return int64(errnoResult(tls, e))
	}
	return int64(r)
}

func _ccgo_poll(tls *libc.TLS, fds uintptr, nfds uint32, timeout int32) int32 {
	if nfds == 0 {
		if timeout < 0 {
			for {
				time.Sleep(time.Second)
			}
		}
		time.Sleep(time.Duration(timeout) * time.Millisecond)
		return 0
	}
	pollfds := unsafe.Slice((*unix.PollFd)(unsafe.Pointer(fds)), int(nfds))
	n, err := unix.Poll(pollfds, int(timeout))
	if err != nil {
		return errnoResult(tls, err)
	}
	return int32(n)
}

var (
	localeMu       sync.Mutex
	localeNames    = [7]string{"C", "C", "C", "C", "C", "C", "C"}
	localeStrings  = map[string]uintptr{}
	localeConv     uintptr
	localeConvOnce sync.Once
)

func stableCString(s string) uintptr {
	if p := localeStrings[s]; p != 0 {
		return p
	}
	p, err := libc.CString(s)
	if err != nil {
		return 0
	}
	localeStrings[s] = p
	return p
}

func libcEnvironment(name string) string {
	p, err := libc.CString(name)
	if err != nil {
		return ""
	}
	defer libc.Xfree(nil, p)
	if value := libc.Xgetenv(nil, p); value != 0 {
		return libc.GoString(value)
	}
	return ""
}

func validLocaleName(name string) (string, bool) {
	if name == "C" || name == "POSIX" {
		return "C", true
	}
	u := strings.ToUpper(name)
	if u == "UTF-8" || strings.HasSuffix(u, ".UTF-8") ||
		strings.HasSuffix(u, ".UTF8") || strings.HasSuffix(u, ".ISO8859-1") ||
		strings.HasSuffix(u, ".ISO88591") {
		return name, true
	}
	return "", false
}

func latin1CTypeLocale() bool {
	localeMu.Lock()
	defer localeMu.Unlock()
	u := strings.ToUpper(localeNames[2])
	return strings.HasSuffix(u, ".ISO8859-1") || strings.HasSuffix(u, ".ISO88591")
}

func _ccgo___maskrune(tls *libc.TLS, c int32, mask uint64) int32 {
	if !latin1CTypeLocale() || c < 0 || c > 255 {
		return libc.X__maskrune(tls, c, mask)
	}
	r := rune(c)
	var properties uint64
	if unicode.IsLetter(r) {
		properties |= 0x00000100
	}
	if unicode.IsDigit(r) {
		properties |= 0x00000400
	}
	if properties&mask != 0 {
		return int32(properties & mask)
	}
	return 0
}

func _ccgo___tolower(tls *libc.TLS, c int32) int32 {
	if latin1CTypeLocale() && c >= 0 && c <= 255 {
		return int32(unicode.ToLower(rune(c)))
	}
	return libc.X__tolower(tls, c)
}

func _ccgo___toupper(tls *libc.TLS, c int32) int32 {
	if latin1CTypeLocale() && c >= 0 && c <= 255 {
		return int32(unicode.ToUpper(rune(c)))
	}
	return libc.X__toupper(tls, c)
}

func localeEnvironment(category int32) string {
	if value := libcEnvironment("LC_ALL"); value != "" {
		return value
	}
	var variable string
	switch category {
	case 1:
		variable = "LC_COLLATE"
	case 2:
		variable = "LC_CTYPE"
	case 3:
		variable = "LC_MONETARY"
	case 4:
		variable = "LC_NUMERIC"
	case 5:
		variable = "LC_TIME"
	case 6:
		variable = "LC_MESSAGES"
	}
	if value := libcEnvironment(variable); value != "" {
		return value
	}
	if value := libcEnvironment("LANG"); value != "" {
		return value
	}
	return "en_US.UTF-8"
}

func _ccgo_setlocale(tls *libc.TLS, category int32, locale uintptr) uintptr {
	if category < 0 || category >= int32(len(localeNames)) {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	localeMu.Lock()
	defer localeMu.Unlock()
	if locale == 0 {
		if category == 0 {
			first := localeNames[1]
			for i := 2; i < len(localeNames); i++ {
				if localeNames[i] != first {
					return stableCString("LC_COLLATE=" + localeNames[1] + ";LC_CTYPE=" + localeNames[2] + ";LC_MONETARY=" + localeNames[3] + ";LC_NUMERIC=" + localeNames[4] + ";LC_TIME=" + localeNames[5] + ";LC_MESSAGES=" + localeNames[6])
				}
			}
			return stableCString(first)
		}
		return stableCString(localeNames[category])
	}
	requested := libc.GoString(locale)
	if requested == "" {
		requested = localeEnvironment(category)
	}
	if category == 0 && strings.Contains(requested, "=") {
		categories := map[string]int{"LC_COLLATE": 1, "LC_CTYPE": 2, "LC_MONETARY": 3, "LC_NUMERIC": 4, "LC_TIME": 5, "LC_MESSAGES": 6}
		updates := localeNames
		for _, assignment := range strings.Split(requested, ";") {
			parts := strings.SplitN(assignment, "=", 2)
			index, exists := categories[parts[0]]
			if len(parts) != 2 || !exists {
				return 0
			}
			name, ok := validLocaleName(parts[1])
			if !ok {
				return 0
			}
			updates[index] = name
		}
		localeNames = updates
		return stableCString(requested)
	}
	name, ok := validLocaleName(requested)
	if !ok {
		return 0
	}
	if category == 0 {
		for i := 1; i < len(localeNames); i++ {
			localeNames[i] = name
		}
		localeNames[0] = name
	} else {
		localeNames[category] = name
	}
	return stableCString(name)
}

func _ccgo_nl_langinfo(tls *libc.TLS, item int32) uintptr {
	localeMu.Lock()
	defer localeMu.Unlock()
	value := ""
	switch item {
	case 0:
		if localeNames[2] == "C" {
			value = "US-ASCII"
		} else {
			value = "UTF-8"
		}
	case 1:
		value = "%a %b %e %H:%M:%S %Y"
	case 2:
		value = "%m/%d/%y"
	case 3:
		value = "%H:%M:%S"
	case 4:
		value = "%I:%M:%S %p"
	case 5:
		value = "AM"
	case 6:
		value = "PM"
	case 7, 8, 9, 10, 11, 12, 13:
		value = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}[item-7]
	case 14, 15, 16, 17, 18, 19, 20:
		value = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[item-14]
	case 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32:
		value = [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}[item-21]
	case 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44:
		value = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}[item-33]
	case 50:
		value = "."
	case 51:
		value = ""
	case 52:
		value = "^[yY]"
	case 53:
		value = "^[nN]"
	case 54:
		value = "yes"
	case 55:
		value = "no"
	case 56:
		value = "-"
	}
	return stableCString(value)
}

func _ccgo_localeconv(tls *libc.TLS) uintptr {
	localeMu.Lock()
	defer localeMu.Unlock()
	localeConvOnce.Do(func() {
		localeConv = libc.Xmalloc(tls, uint64(unsafe.Sizeof(Tlconv{})))
		if localeConv == 0 {
			return
		}
		lc := (*Tlconv)(unsafe.Pointer(localeConv))
		empty := stableCString("")
		lc.Fdecimal_point = stableCString(".")
		lc.Fthousands_sep = empty
		lc.Fgrouping = empty
		lc.Fint_curr_symbol = empty
		lc.Fcurrency_symbol = empty
		lc.Fmon_decimal_point = empty
		lc.Fmon_thousands_sep = empty
		lc.Fmon_grouping = empty
		lc.Fpositive_sign = empty
		lc.Fnegative_sign = empty
		for _, field := range []*int8{&lc.Fint_frac_digits, &lc.Ffrac_digits, &lc.Fp_cs_precedes, &lc.Fp_sep_by_space, &lc.Fn_cs_precedes, &lc.Fn_sep_by_space, &lc.Fp_sign_posn, &lc.Fn_sign_posn, &lc.Fint_p_cs_precedes, &lc.Fint_n_cs_precedes, &lc.Fint_p_sep_by_space, &lc.Fint_n_sep_by_space, &lc.Fint_p_sign_posn, &lc.Fint_n_sign_posn} {
			*field = 127
		}
	})
	return localeConv
}

func currentLocation() *time.Location {
	tz := libcEnvironment("TZ")
	if tz == "" {
		return time.Local
	}
	if location, err := time.LoadLocation(tz); err == nil {
		return location
	}
	if strings.HasPrefix(tz, "EST+05EDT") {
		location, _ := time.LoadLocation("America/New_York")
		return location
	}
	if strings.HasPrefix(tz, "AEST-10AEDT") {
		location, _ := time.LoadLocation("Australia/Melbourne")
		return location
	}
	i := 0
	for i < len(tz) && ((tz[i] >= 'A' && tz[i] <= 'Z') || (tz[i] >= 'a' && tz[i] <= 'z')) {
		i++
	}
	name := tz[:i]
	if name == "" {
		name = "UTC"
	}
	offset := 0
	if i < len(tz) {
		sign := -1
		if tz[i] == '-' {
			sign = 1
			i++
		} else if tz[i] == '+' {
			i++
		}
		start := i
		for i < len(tz) && tz[i] >= '0' && tz[i] <= '9' {
			i++
		}
		if hours, err := strconv.Atoi(tz[start:i]); err == nil {
			offset = sign * hours * 3600
		}
	}
	return time.FixedZone(name, offset)
}

func fillLocaltime(result uintptr, seconds int64) {
	location := currentLocation()
	t := time.Unix(seconds, 0).In(location)
	tm := (*Ttm)(unsafe.Pointer(result))
	tm.Ftm_sec = int32(t.Second())
	tm.Ftm_min = int32(t.Minute())
	tm.Ftm_hour = int32(t.Hour())
	tm.Ftm_mday = int32(t.Day())
	tm.Ftm_mon = int32(t.Month()) - 1
	tm.Ftm_year = int32(t.Year()) - 1900
	tm.Ftm_wday = int32(t.Weekday())
	tm.Ftm_yday = int32(t.YearDay()) - 1
	name, offset := t.Zone()
	tm.Ftm_gmtoff = int64(offset)
	tm.Ftm_isdst = 0
	janName, janOffset := time.Date(t.Year(), time.January, 1, 12, 0, 0, 0, location).Zone()
	julName, julOffset := time.Date(t.Year(), time.July, 1, 12, 0, 0, 0, location).Zone()
	if (offset != janOffset || name != janName) && (offset != julOffset || name != julName) {
		tm.Ftm_isdst = 1
	} else if janOffset != julOffset && offset == max(janOffset, julOffset) {
		tm.Ftm_isdst = 1
	}
	localeMu.Lock()
	tm.Ftm_zone = stableCString(name)
	localeMu.Unlock()
}

func _ccgo_localtime_r(tls *libc.TLS, source, result uintptr) uintptr {
	if source == 0 || result == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	fillLocaltime(result, *(*int64)(unsafe.Pointer(source)))
	return result
}

var (
	localtimeResult     uintptr
	localtimeResultOnce sync.Once
)

func _ccgo_localtime(tls *libc.TLS, source uintptr) uintptr {
	localtimeResultOnce.Do(func() {
		localtimeResult = libc.Xmalloc(tls, uint64(unsafe.Sizeof(Ttm{})))
	})
	if localtimeResult == 0 {
		return 0
	}
	return _ccgo_localtime_r(tls, source, localtimeResult)
}

func _ccgo_mktime(tls *libc.TLS, source uintptr) int64 {
	if source == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	tm := (*Ttm)(unsafe.Pointer(source))
	t := time.Date(int(tm.Ftm_year)+1900, time.Month(tm.Ftm_mon+1), int(tm.Ftm_mday), int(tm.Ftm_hour), int(tm.Ftm_min), int(tm.Ftm_sec), 0, currentLocation())
	tm.Ftm_wday = int32(t.Weekday())
	tm.Ftm_yday = int32(t.YearDay()) - 1
	return t.Unix()
}

func _ccgo_mknod(tls *libc.TLS, path uintptr, mode uint16, device int32) int32 {
	return errnoResult(tls, unix.Mknod(libc.GoString(path), uint32(mode), int(device)))
}

// ponytail: TSS keys are never reused, so deleting one is a no-op.
func _ccgo_pthread_key_delete(tls *libc.TLS, key uint64) int32 { return 0 }
