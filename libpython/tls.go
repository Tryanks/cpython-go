// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package libpython

import (
	"sync"
	"sync/atomic"

	"modernc.org/libc"
)

// C `_Thread_local` variables. ccgo lowers them to plain globals, which
// breaks CPython as soon as a second thread attaches (every thread has its
// own PyThreadState). generator.go rewrites each use of the listed variables
// to (*_ccgo_tls_<name>(tls)): one slot per libc.TLS, indexed by tls.ID.
type tlsVars struct {
	tstate     uintptr // _Py_tss_tstate (Python/pystate.c)
	pkgcontext uintptr // pkgcontext (Python/import.c)
}

var (
	tlsTable atomic.Pointer[[]*tlsVars] // copy-on-write; entries are stable
	tlsMu    sync.Mutex
)

func tlsVarsOf(tls *libc.TLS) *tlsVars {
	id := int(tls.ID)
	if t := tlsTable.Load(); t != nil && id < len(*t) {
		if v := (*t)[id]; v != nil {
			return v
		}
	}
	return tlsVarsSlow(id)
}

func tlsVarsSlow(id int) *tlsVars {
	tlsMu.Lock()
	defer tlsMu.Unlock()
	var old []*tlsVars
	if t := tlsTable.Load(); t != nil {
		old = *t
	}
	if id < len(old) && old[id] != nil {
		return old[id]
	}
	n := len(old)
	if id >= n {
		n = id + 1 + id/2
	}
	s := make([]*tlsVars, n) // ponytail: slots of exited threads are never freed (16 bytes each)
	copy(s, old)
	s[id] = &tlsVars{}
	tlsTable.Store(&s)
	return s[id]
}

func _ccgo_tls_tstate(tls *libc.TLS) *uintptr     { return &tlsVarsOf(tls).tstate }
func _ccgo_tls_pkgcontext(tls *libc.TLS) *uintptr { return &tlsVarsOf(tls).pkgcontext }
