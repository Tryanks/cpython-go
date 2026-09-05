// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"modernc.org/libc"
)

// Signal handling shared by the per-OS sigaction shims. Handlers installed
// by C are recorded here; a Go os/signal subscription per signal invokes the
// C handler on a dedicated libc.TLS. CPython's signal_handler only sets flags
// and writes to the wakeup fd, so calling it from a goroutine is safe. A
// process signalling itself (kill/raise) runs the handler synchronously.
type goSigaction struct {
	handler uintptr
	mask    Tsigset_t
	flags   int32
	ch      chan os.Signal
	owner   *libc.TLS
}

var (
	sigactionMu    sync.Mutex
	sigactionTable = map[int32]goSigaction{}
	signalStateMu  sync.Mutex
	signalMasks    = map[*libc.TLS]uint64{}
	pendingSignals = map[*libc.TLS]uint64{}
	signalWake     = make(chan struct{})
)

// installSigaction records next (nil = query only) and returns the previous
// action.
func installSigaction(signum int32, next *goSigaction) goSigaction {
	sig := syscall.Signal(signum)
	sigactionMu.Lock()
	defer sigactionMu.Unlock()
	previous := sigactionTable[signum]
	if next == nil {
		return previous
	}
	if previous.ch != nil {
		signal.Stop(previous.ch)
		close(previous.ch) // lets the dispatcher goroutine exit
	}
	switch next.handler {
	case 0: // SIG_DFL
		signal.Reset(sig)
	case 1: // SIG_IGN
		signal.Ignore(sig)
	default:
		next.ch = make(chan os.Signal, 1)
		signal.Notify(next.ch, sig)
		owner, ch := next.owner, next.ch
		go func() {
			// The C handler must never run on the owner's libc.TLS: this
			// goroutine executes concurrently with the owner thread and
			// tls.Alloc/Free on a shared TLS corrupts the owner's C stack
			// frames. owner is only consulted for its signal mask.
			dtls := libc.NewTLS()
			defer dtls.Close()
			for received := range ch {
				dispatchSignalOn(owner, dtls, int32(received.(syscall.Signal)))
			}
		}()
	}
	sigactionTable[signum] = *next
	return previous
}

// selfSignal runs the installed C handler for sig synchronously and reports
// whether the signal was consumed (ignored or handled).
func selfSignal(tls *libc.TLS, sig int32) bool {
	return dispatchSignal(tls, sig)
}

func dispatchSignal(tls *libc.TLS, sig int32) bool {
	return dispatchSignalOn(tls, tls, sig)
}

// dispatchSignalOn queues sig if it is blocked for tls (or another blocked
// thread) and otherwise runs the C handler on exec.
func dispatchSignalOn(tls, exec *libc.TLS, sig int32) bool {
	bit := signalBit(sig)
	signalStateMu.Lock()
	target := tls
	if signalMasks[target]&bit == 0 {
		// pthread masks are inherited by newly-created threads. libc.TLS has
		// no construction hook, so select an existing blocked owner when a
		// process-directed signal arrives from a newly-created TLS.
		for candidate, mask := range signalMasks {
			if mask&bit != 0 {
				target = candidate
				break
			}
		}
	}
	if signalMasks[target]&bit != 0 {
		pendingSignals[target] |= bit
		close(signalWake)
		signalWake = make(chan struct{})
		signalStateMu.Unlock()
		return true
	}
	signalStateMu.Unlock()
	return deliverSignal(exec, sig)
}

func deliverSignal(tls *libc.TLS, sig int32) bool {
	sigactionMu.Lock()
	action, exists := sigactionTable[sig]
	sigactionMu.Unlock()
	if !exists {
		return false
	}
	switch action.handler {
	case 1:
		return true
	case 0:
		return false
	default:
		fn := *(*func(*libc.TLS, int32))(unsafe.Pointer(&struct{ uintptr }{action.handler}))
		fn(tls, sig)
		noteSignalDelivery()
		return true
	}
}

func signalBit(sig int32) uint64 {
	if sig <= 0 || sig > 64 {
		return 0
	}
	return uint64(1) << uint(sig-1)
}

func readSignalSet(p uintptr) uint64 {
	if p == 0 {
		return 0
	}
	var set Tsigset_t
	b := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(unsafe.Sizeof(set)))
	if len(b) > 8 {
		b = b[:8]
	}
	var word uint64
	for i, v := range b {
		word |= uint64(v) << uint(8*i)
	}
	return word
}

func writeSignalSet(p uintptr, word uint64) {
	if p == 0 {
		return
	}
	var set Tsigset_t
	b := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(unsafe.Sizeof(set)))
	clear(b)
	for i := 0; i < len(b) && i < 8; i++ {
		b[i] = byte(word >> uint(8*i))
	}
}

func signalMaskOps() (block, unblock, setmask int32) {
	if runtime.GOOS == "darwin" {
		return 1, 2, 3
	}
	return 0, 1, 2
}

func _ccgo_pthread_sigmask(tls *libc.TLS, how int32, set, old uintptr) int32 {
	block, unblock, setmask := signalMaskOps()
	signalStateMu.Lock()
	previous := signalMasks[tls]
	writeSignalSet(old, previous)
	if set == 0 {
		signalStateMu.Unlock()
		return 0
	}
	switch how {
	case block:
		signalMasks[tls] |= readSignalSet(set)
	case unblock:
		signalMasks[tls] &^= readSignalSet(set)
	case setmask:
		signalMasks[tls] = readSignalSet(set)
	default:
		signalStateMu.Unlock()
		return int32(syscall.EINVAL)
	}
	deliver := pendingSignals[tls] &^ signalMasks[tls]
	pendingSignals[tls] &^= deliver
	signalStateMu.Unlock()
	for sig := int32(1); sig <= 64; sig++ {
		if deliver&signalBit(sig) != 0 {
			deliverSignal(tls, sig)
		}
	}
	return 0
}

func _ccgo_sigpending(tls *libc.TLS, set uintptr) int32 {
	signalStateMu.Lock()
	pending := pendingSignals[tls]
	signalStateMu.Unlock()
	writeSignalSet(set, pending)
	return 0
}

func waitPendingSignal(tls *libc.TLS, set uintptr, timeout time.Duration) (int32, bool) {
	wanted := readSignalSet(set)
	deadline := time.Now().Add(timeout)
	for {
		signalStateMu.Lock()
		available := pendingSignals[tls] & wanted
		if available != 0 {
			sig := int32(1)
			for available&signalBit(sig) == 0 {
				sig++
			}
			pendingSignals[tls] &^= signalBit(sig)
			signalStateMu.Unlock()
			return sig, true
		}
		wake := signalWake
		signalStateMu.Unlock()
		if timeout < 0 {
			<-wake
			continue
		}
		left := time.Until(deadline)
		if left <= 0 {
			return 0, false
		}
		select {
		case <-wake:
		case <-time.After(left):
			return 0, false
		}
	}
}

func _ccgo_sigwait(tls *libc.TLS, set, sigp uintptr) int32 {
	sig, _ := waitPendingSignal(tls, set, -1)
	*(*int32)(unsafe.Pointer(sigp)) = sig
	return 0
}

func _ccgo_sigwaitinfo(tls *libc.TLS, set, info uintptr) int32 {
	sig, _ := waitPendingSignal(tls, set, -1)
	if info != 0 {
		*(*Tsiginfo_t)(unsafe.Pointer(info)) = Tsiginfo_t{}
		*(*int32)(unsafe.Pointer(info)) = sig
	}
	return sig
}

func _ccgo_sigtimedwait(tls *libc.TLS, set, info, timeout uintptr) int32 {
	ts := (*Ttimespec)(unsafe.Pointer(timeout))
	d := time.Duration(ts.Ftv_sec)*time.Second + time.Duration(ts.Ftv_nsec)
	sig, ok := waitPendingSignal(tls, set, d)
	if !ok {
		setErrno(tls, int32(syscall.EAGAIN))
		return -1
	}
	if info != 0 {
		*(*Tsiginfo_t)(unsafe.Pointer(info)) = Tsiginfo_t{}
		*(*int32)(unsafe.Pointer(info)) = sig
	}
	return sig
}

func _ccgo_siginterrupt(tls *libc.TLS, sig, interrupt int32) int32 { return 0 }
