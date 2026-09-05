// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package libpython

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	"modernc.org/libc/errno"
)

// Functions the transpiled musl in modernc.org/libc does not provide on
// linux. errno is reported through libc's per-TLS errno location.

func linuxErrno(tls *libc.TLS, err error) int32 {
	if err == nil {
		return 0
	}
	if e, ok := err.(unix.Errno); ok {
		*(*int32)(unsafe.Pointer(libc.X__errno_location(tls))) = int32(e)
	}
	return -1
}

var (
	linuxLocaleMu      sync.Mutex
	linuxLocaleNames   = [7]string{"C", "C", "C", "C", "C", "C", "C"}
	linuxLocaleStrings = map[string]uintptr{}
)

func linuxLocaleCString(s string) uintptr {
	if p := linuxLocaleStrings[s]; p != 0 {
		return p
	}
	p, _ := libc.CString(s)
	linuxLocaleStrings[s] = p
	return p
}

func linuxLocaleSupported(name string) bool {
	u := strings.ToUpper(name)
	return name == "C" || name == "POSIX" || u == "UTF-8" ||
		strings.HasSuffix(u, ".UTF-8") || strings.HasSuffix(u, ".UTF8") ||
		strings.HasSuffix(u, ".ISO8859-1") || strings.HasSuffix(u, ".ISO88591")
}

func _ccgo_setlocale(tls *libc.TLS, category int32, locale uintptr) uintptr {
	if category < 0 || category >= int32(len(linuxLocaleNames)) {
		setErrno(tls, int32(errno.EINVAL))
		return 0
	}
	linuxLocaleMu.Lock()
	defer linuxLocaleMu.Unlock()
	if locale == 0 {
		return linuxLocaleCString(linuxLocaleNames[category])
	}
	name := libc.GoString(locale)
	if name == "" {
		name = "en_US.UTF-8"
	}
	if !linuxLocaleSupported(name) {
		return 0
	}
	if name == "POSIX" {
		name = "C"
	}
	if category == 6 { // Linux LC_ALL
		for i := range linuxLocaleNames {
			linuxLocaleNames[i] = name
		}
	} else {
		linuxLocaleNames[category] = name
	}
	return linuxLocaleCString(name)
}

func linuxLatin1CType() bool {
	linuxLocaleMu.Lock()
	defer linuxLocaleMu.Unlock()
	u := strings.ToUpper(linuxLocaleNames[0]) // Linux LC_CTYPE
	return strings.HasSuffix(u, ".ISO8859-1") || strings.HasSuffix(u, ".ISO88591")
}

func _ccgo_isalnum(tls *libc.TLS, c int32) int32 {
	if linuxLatin1CType() && c >= 0 && c <= 255 && (unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))) {
		return 1
	}
	return libc.Xisalnum(tls, c)
}

func _ccgo_tolower(tls *libc.TLS, c int32) int32 {
	if linuxLatin1CType() && c >= 0 && c <= 255 {
		return int32(unicode.ToLower(rune(c)))
	}
	return libc.Xtolower(tls, c)
}

func _ccgo_toupper(tls *libc.TLS, c int32) int32 {
	if linuxLatin1CType() && c >= 0 && c <= 255 {
		return int32(unicode.ToUpper(rune(c)))
	}
	return libc.Xtoupper(tls, c)
}

func _seteuid(tls *libc.TLS, uid uint32) int32 { return linuxErrno(tls, syscall.Seteuid(int(uid))) }
func _setegid(tls *libc.TLS, gid uint32) int32 { return linuxErrno(tls, syscall.Setegid(int(gid))) }
func _setreuid(tls *libc.TLS, r, e uint32) int32 {
	return linuxErrno(tls, unix.Setreuid(int(r), int(e)))
}
func _setregid(tls *libc.TLS, r, e uint32) int32 {
	return linuxErrno(tls, unix.Setregid(int(r), int(e)))
}
func _setresuid(tls *libc.TLS, r, e, s uint32) int32 {
	return linuxErrno(tls, unix.Setresuid(int(r), int(e), int(s)))
}
func _setresgid(tls *libc.TLS, r, e, s uint32) int32 {
	return linuxErrno(tls, unix.Setresgid(int(r), int(e), int(s)))
}
func _setgroups(tls *libc.TLS, n uint64, list uintptr) int32 {
	gids := make([]int, n)
	for i := range gids {
		gids[i] = int(*(*uint32)(unsafe.Pointer(list + uintptr(i)*4)))
	}
	return linuxErrno(tls, unix.Setgroups(gids))
}

// ponytail: initgroups needs the group database; supplementary groups are
// not set (same as a system without /etc/group).
func _initgroups(tls *libc.TLS, user uintptr, gid uint32) int32 {
	return linuxErrno(tls, unix.Setgroups([]int{int(gid)}))
}

// ponytail: no services database; socket.getservbyport raises OSError.
func _getservbyport(tls *libc.TLS, port int32, proto uintptr) uintptr { return 0 }

// FLT_ROUNDS (musl: __flt_rounds()) — round to nearest.
func ___flt_rounds(tls *libc.TLS) int32 { return 1 }

// POSIX semaphores (Python/thread_pthread.h, parking_lot.c). The transpiled
// musl has none; a counting semaphore per sem_t address.
// ponytail: buffered channel, at most 1<<20 outstanding posts.
var (
	semMu sync.Mutex
	sems  = map[uintptr]chan struct{}{}
)

func semOf(sem uintptr) chan struct{} {
	semMu.Lock()
	defer semMu.Unlock()
	return sems[sem]
}

func _sem_init(tls *libc.TLS, sem uintptr, pshared int32, value uint32) int32 {
	c := make(chan struct{}, 1<<20)
	for i := uint32(0); i < value; i++ {
		c <- struct{}{}
	}
	semMu.Lock()
	sems[sem] = c
	semMu.Unlock()
	return 0
}

func _sem_destroy(tls *libc.TLS, sem uintptr) int32 {
	semMu.Lock()
	delete(sems, sem)
	semMu.Unlock()
	return 0
}

func _sem_post(tls *libc.TLS, sem uintptr) int32 {
	semOf(sem) <- struct{}{}
	return 0
}

func _sem_wait(tls *libc.TLS, sem uintptr) int32 {
	<-semOf(sem)
	return 0
}

func _sem_trywait(tls *libc.TLS, sem uintptr) int32 {
	select {
	case <-semOf(sem):
		return 0
	default:
		setErrno(tls, int32(errno.EAGAIN))
		return -1
	}
}

func _sem_timedwait(tls *libc.TLS, sem, abstime uintptr) int32 {
	ts := (*Ttimespec)(unsafe.Pointer(abstime))
	d := time.Until(time.Unix(int64(ts.Ftv_sec), int64(ts.Ftv_nsec)))
	if d <= 0 {
		return _sem_trywait(tls, sem)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-semOf(sem):
		return 0
	case <-t.C:
		setErrno(tls, int32(errno.ETIMEDOUT))
		return -1
	}
}

// ponytail: thread names are not tracked.
func _pthread_setname_np(tls *libc.TLS, thread, name uintptr) int32 { return 0 }
func _pthread_getname_np(tls *libc.TLS, thread, buf uintptr, n uint64) int32 {
	if n > 0 {
		*(*byte)(unsafe.Pointer(buf)) = 0
	}
	return 0
}

// Not available under the Go runtime (see fork_exec.go for subprocess).
func _posix_spawn(tls *libc.TLS, pid, path, fa, attr, argv, envp uintptr) int32 {
	return int32(errno.ENOSYS)
}
func _posix_spawnp(tls *libc.TLS, pid, file, fa, attr, argv, envp uintptr) int32 {
	return int32(errno.ENOSYS)
}
func _forkpty(tls *libc.TLS, master, name, termios, winsize uintptr) int32 {
	setErrno(tls, int32(errno.ENOSYS))
	return -1
}

// Routed here by generator.go (shimmedLibc["linux"]): musl's stdio locking in
// modernc.org/libc aborts on an unsupported asm barrier. No-ops.
func _ccgo_flockfile(tls *libc.TLS, f uintptr)   {}
func _ccgo_funlockfile(tls *libc.TLS, f uintptr) {}

// Routed here by generator.go (shimmedLibc["linux"]): musl's sigaction in
// modernc.org/libc aborts on an unsupported asm barrier.
func _ccgo_sigaction(tls *libc.TLS, signum int32, act, oldact uintptr) int32 {
	if signum <= 0 || signum == int32(syscall.SIGKILL) || signum == int32(syscall.SIGSTOP) {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	var next *goSigaction
	if act != 0 {
		a := (*Tsigaction)(unsafe.Pointer(act))
		next = &goSigaction{handler: a.F__sa_handler.Fsa_handler, mask: a.Fsa_mask, flags: a.Fsa_flags, owner: tls}
	}
	previous := installSigaction(signum, next)
	if oldact != 0 {
		a := (*Tsigaction)(unsafe.Pointer(oldact))
		a.F__sa_handler.Fsa_handler = previous.handler
		a.Fsa_mask = previous.mask
		a.Fsa_flags = previous.flags
	}
	return 0
}

func _ccgo_kill(tls *libc.TLS, pid, sig int32) int32 {
	if pid == int32(unix.Getpid()) && selfSignal(tls, sig) {
		return 0
	}
	return linuxErrno(tls, unix.Kill(int(pid), syscall.Signal(sig)))
}

func _ccgo_raise(tls *libc.TLS, sig int32) int32 { return _ccgo_kill(tls, int32(unix.Getpid()), sig) }

func _ccgo_clock_nanosleep(tls *libc.TLS, clock, flags int32, request, remainder uintptr) int32 {
	const timerAbsTime = 1
	req := (*Ttimespec)(unsafe.Pointer(request))
	if req.Ftv_sec < 0 || req.Ftv_nsec < 0 || req.Ftv_nsec >= int64(time.Second) {
		return int32(errno.EINVAL)
	}
	if consumeSignalDelivery() {
		return int32(errno.EINTR)
	}
	for {
		var duration time.Duration
		if flags&timerAbsTime != 0 {
			var now unix.Timespec
			if err := unix.ClockGettime(clock, &now); err != nil {
				return int32(syscallErrno(err))
			}
			duration = time.Duration(req.Ftv_sec-now.Sec)*time.Second + time.Duration(req.Ftv_nsec-now.Nsec)
		} else {
			duration = time.Duration(req.Ftv_sec)*time.Second + time.Duration(req.Ftv_nsec)
		}
		if duration <= 0 {
			return 0
		}
		if duration > 10*time.Millisecond {
			duration = 10 * time.Millisecond
		}
		time.Sleep(duration)
		if consumeSignalDelivery() {
			if remainder != 0 && flags&timerAbsTime == 0 {
				rem := (*Ttimespec)(unsafe.Pointer(remainder))
				rem.Ftv_sec = 0
				rem.Ftv_nsec = 0
			}
			return int32(errno.EINTR)
		}
	}
}

type linuxIntervalTimer struct {
	timer    *time.Timer
	deadline time.Time
	repeat   time.Duration
}

var (
	linuxIntervalMu     sync.Mutex
	linuxIntervalTimers [3]linuxIntervalTimer
)

func linuxIntervalValue(which int32, value uintptr) int32 {
	if which < 0 || which >= int32(len(linuxIntervalTimers)) || value == 0 {
		return -1
	}
	state := &linuxIntervalTimers[which]
	it := (*Titimerval)(unsafe.Pointer(value))
	*it = Titimerval{}
	remaining := time.Until(state.deadline)
	if state.timer == nil || remaining < 0 {
		remaining = 0
	}
	it.Fit_value.Ftv_sec = int64(remaining / time.Second)
	it.Fit_value.Ftv_usec = int64(remaining % time.Second / time.Microsecond)
	it.Fit_interval.Ftv_sec = int64(state.repeat / time.Second)
	it.Fit_interval.Ftv_usec = int64(state.repeat % time.Second / time.Microsecond)
	return 0
}

func _ccgo_getitimer(tls *libc.TLS, which int32, value uintptr) int32 {
	linuxIntervalMu.Lock()
	defer linuxIntervalMu.Unlock()
	if linuxIntervalValue(which, value) != 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	return 0
}

func _ccgo_setitimer(tls *libc.TLS, which int32, value, old uintptr) int32 {
	if which < 0 || which >= int32(len(linuxIntervalTimers)) || value == 0 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	it := (*Titimerval)(unsafe.Pointer(value))
	if it.Fit_value.Ftv_sec < 0 || it.Fit_value.Ftv_usec < 0 || it.Fit_value.Ftv_usec >= 1_000_000 || it.Fit_interval.Ftv_sec < 0 || it.Fit_interval.Ftv_usec < 0 || it.Fit_interval.Ftv_usec >= 1_000_000 {
		setErrno(tls, int32(errno.EINVAL))
		return -1
	}
	initial := time.Duration(it.Fit_value.Ftv_sec)*time.Second + time.Duration(it.Fit_value.Ftv_usec)*time.Microsecond
	repeat := time.Duration(it.Fit_interval.Ftv_sec)*time.Second + time.Duration(it.Fit_interval.Ftv_usec)*time.Microsecond

	linuxIntervalMu.Lock()
	defer linuxIntervalMu.Unlock()
	if old != 0 {
		linuxIntervalValue(which, old)
	}
	state := &linuxIntervalTimers[which]
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	state.repeat = repeat
	if initial == 0 {
		return 0
	}
	signalNumber := [...]int32{int32(syscall.SIGALRM), int32(syscall.SIGVTALRM), int32(syscall.SIGPROF)}[which]
	var fire func()
	fire = func() {
		dtls := libc.NewTLS()
		selfSignal(dtls, signalNumber)
		dtls.Close()
		noteSignalDelivery()
		linuxIntervalMu.Lock()
		defer linuxIntervalMu.Unlock()
		current := &linuxIntervalTimers[which]
		if current.repeat != 0 {
			current.deadline = time.Now().Add(current.repeat)
			current.timer = time.AfterFunc(current.repeat, fire)
		} else {
			current.timer = nil
		}
	}
	state.deadline = time.Now().Add(initial)
	state.timer = time.AfterFunc(initial, fire)
	return 0
}

func _ccgo_pause(tls *libc.TLS) int32 {
	generation := deliveredSignalGeneration.Load()
	for deliveredSignalGeneration.Load() == generation {
		time.Sleep(10 * time.Millisecond)
	}
	observedSignalGeneration.Store(deliveredSignalGeneration.Load())
	setErrno(tls, int32(errno.EINTR))
	return -1
}

func _ccgo_syscall(tls *libc.TLS, number int64, args uintptr) int64 {
	const sysPidfdSendSignal = 424
	if number != sysPidfdSendSignal {
		return libc.Xsyscall(tls, number, args)
	}
	ap := args
	pidfd := int(libc.VaInt64(&ap))
	signalNumber := int32(libc.VaInt64(&ap))
	siginfo := uintptr(libc.VaInt64(&ap))
	flags := int32(libc.VaInt64(&ap))
	if siginfo == 0 && flags == 0 {
		if target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", pidfd)); err == nil && target == fmt.Sprintf("/proc/%d", unix.Getpid()) {
			if selfSignal(tls, signalNumber) {
				return 0
			}
		}
	}
	return libc.Xsyscall(tls, number, args)
}
