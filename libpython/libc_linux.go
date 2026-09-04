// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package libpython

import (
	"sync"
	"syscall"
	"time"
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
		next = &goSigaction{handler: a.F__sa_handler.Fsa_handler, mask: a.Fsa_mask, flags: a.Fsa_flags}
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
