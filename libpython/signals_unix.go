// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
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
}

var (
	sigactionMu    sync.Mutex
	sigactionTable = map[int32]goSigaction{}
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
	}
	switch next.handler {
	case 0: // SIG_DFL
		signal.Reset(sig)
	case 1: // SIG_IGN
		signal.Ignore(sig)
	default:
		next.ch = make(chan os.Signal, 1)
		signal.Notify(next.ch, sig)
		handler, ch := next.handler, next.ch
		go func() {
			dtls := libc.NewTLS()
			defer dtls.Close()
			fn := *(*func(*libc.TLS, int32))(unsafe.Pointer(&struct{ uintptr }{handler}))
			for received := range ch {
				fn(dtls, int32(received.(syscall.Signal)))
			}
		}()
	}
	sigactionTable[signum] = *next
	return previous
}

// selfSignal runs the installed C handler for sig synchronously and reports
// whether the signal was consumed (ignored or handled).
func selfSignal(tls *libc.TLS, sig int32) bool {
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
		return true
	}
}
