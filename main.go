// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
	"github.com/Tryanks/cpython-go/stdlib"
)

// Main runs the python3 command line with args, args[0] being the program
// name, and returns the process exit status. It initializes and finalizes its
// own interpreter and must not be mixed with New.
//
// Unless PYTHONHOME is set, the embedded standard library is used; it is
// configured through PyConfig.home rather than the environment so that -E
// and -I (which ignore PYTHON* variables) still find it.
func Main(args []string) int {
	runtime.LockOSThread()
	tls := libc.NewTLS()
	libc.SetEnviron(tls, os.Environ())

	const ptrSize = unsafe.Sizeof(uintptr(0))
	argv := libc.Xmalloc(tls, uint64(ptrSize)*uint64(len(args)+1))
	if argv == 0 {
		panic("cpython: out of memory")
	}
	for i, a := range args {
		p, err := libc.CString(a)
		if err != nil {
			panic(err)
		}
		*(*uintptr)(up(argv + uintptr(i)*ptrSize)) = p
	}
	*(*uintptr)(up(argv + uintptr(len(args))*ptrSize)) = 0

	c := libc.Xmalloc(tls, uint64(unsafe.Sizeof(libpython.TPyConfig{})))
	if c == 0 {
		panic("cpython: out of memory")
	}
	libpython.XPyConfig_InitPythonConfig(tls, c)
	check := func(st libpython.TPyStatus) {
		if libpython.XPyStatus_Exception(tls, st) != 0 {
			libpython.XPyConfig_Clear(tls, c)
			libpython.XPy_ExitStatusException(tls, st) // prints and exits
		}
	}
	check(libpython.XPyConfig_SetBytesArgv(tls, c, int64(len(args)), argv))
	if os.Getenv("PYTHONHOME") == "" {
		home, err := stdlib.Home()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cpython: cannot extract embedded stdlib:", err)
			return 1
		}
		p, _ := libc.CString(home)
		check(libpython.XPyConfig_SetBytesString(tls, c, c+unsafe.Offsetof(libpython.TPyConfig{}.Fhome), p))
		libc.Xfree(tls, p)
	}
	check(libpython.XPy_InitializeFromConfig(tls, c))
	libpython.XPyConfig_Clear(tls, c)
	return int(libpython.XPy_RunMain(tls))
}

var (
	versionOnce sync.Once
	version     string
)

// Version returns the Py_GetVersion string, for example
// "3.12.11 (main, ...) [ccgo]". The first line up to the first space is the
// release number.
func Version() string {
	versionOnce.Do(func() {
		tls := libc.NewTLS()
		version = strings.TrimSpace(libc.GoString(libpython.XPy_GetVersion(tls)))
	})
	return version
}
