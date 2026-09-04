// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
)

// Sentinel errors returned by the package.
var (
	// ErrClosed is returned by every method of an Interpreter that has been
	// closed.
	ErrClosed = errors.New("cpython: interpreter is closed")
	// ErrCrashed is returned by every method of an Interpreter after
	// transpiled C code panicked. The C state is assumed corrupt.
	ErrCrashed = errors.New("cpython: interpreter crashed")
	// ErrUnsupported is returned when a Go value has no Python equivalent.
	ErrUnsupported = errors.New("cpython: unsupported conversion")
	// ErrAlreadyOpen is returned by New while another Interpreter is open.
	ErrAlreadyOpen = errors.New("cpython: an interpreter is already open")
)

// Error is a Python exception that propagated out of the interpreter.
type Error struct {
	Type      string  // exception type name, e.g. "ValueError"
	Message   string  // str(exception)
	Traceback string  // traceback.format_exception output, may be empty
	Exception *Object // the exception object, nil if it could not be retained
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Type
	}
	return e.Type + ": " + e.Message
}

// IsException reports whether err is a Python exception of the named type.
// The comparison is on the type name only, e.g. IsException(err, "KeyError").
func IsException(err error, typeName string) bool {
	var e *Error
	return errors.As(err, &e) && e.Type == typeName
}

// ExitError reports that the Python code called sys.exit, or that the
// embedded C code called exit, _exit or abort.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("cpython: exit status %d", e.Code) }

// ExitCode returns the exit status. It makes ExitError satisfy the interface
// libpython uses to report a C level exit.
func (e *ExitError) ExitCode() int { return e.Code }

// CrashError is a Go panic that escaped the transpiled interpreter. The
// interpreter that produced it is poisoned: every later call returns
// ErrCrashed.
type CrashError struct {
	Value any
	Stack []byte
}

func (e *CrashError) Error() string { return fmt.Sprintf("cpython: interpreter crashed: %v", e.Value) }

// recoverPanic converts a panic from transpiled code into *ExitError (for
// exit, _exit and abort, which libpython reports as a panic value
// implementing ExitCode) or into *CrashError, which also poisons the
// interpreter.
//
// It must be deferred after the leave defer, so that it runs first.
func (in *Interpreter) recoverPanic(errp *error) {
	r := recover()
	if r == nil {
		return
	}
	if e, ok := r.(interface{ ExitCode() int }); ok {
		*errp = &ExitError{Code: e.ExitCode()}
		return
	}
	in.crashed = true
	*errp = &CrashError{Value: r, Stack: debug.Stack()}
}

// pyError builds an error from the currently raised Python exception. It
// returns *ExitError for SystemExit and *Error otherwise. The exception is
// cleared.
func (in *Interpreter) pyError() error {
	exc := libpython.XPyErr_GetRaisedException(in.tls)
	if exc == 0 {
		return &Error{Type: "SystemError", Message: "operation failed without setting an exception"}
	}
	defer libpython.XPy_DecRef(in.tls, exc)

	if libpython.XPyErr_GivenExceptionMatches(in.tls, exc, libpython.XPyExc_SystemExit) != 0 {
		return in.exitError(exc)
	}
	return &Error{
		Type:      in.typeName(exc),
		Message:   in.rawStr(exc),
		Traceback: in.formatException(exc),
		Exception: in.obj(libpython.XPy_NewRef(in.tls, exc)),
	}
}

// exitError maps a SystemExit instance to *ExitError the way CPython's
// handle_system_exit does: None is 0, an int is itself, anything else is
// printed to stderr and becomes 1.
func (in *Interpreter) exitError(exc uintptr) error {
	code := in.getAttr(exc, "code")
	if code == 0 {
		libpython.XPyErr_Clear(in.tls)
		return &ExitError{Code: 0}
	}
	defer libpython.XPy_DecRef(in.tls, code)
	switch {
	case code == none:
		return &ExitError{Code: 0}
	case in.isa(code, &libpython.XPyLong_Type):
		return &ExitError{Code: int(libpython.XPyLong_AsLong(in.tls, code))}
	default:
		fmt.Fprintln(os.Stderr, in.rawStr(code))
		return &ExitError{Code: 1}
	}
}

// formatException renders a traceback with traceback.format_exception. It
// returns "" if the traceback module itself misbehaves.
func (in *Interpreter) formatException(exc uintptr) string {
	if in.formatExc == 0 {
		mod := in.importModule("traceback")
		if mod == 0 {
			libpython.XPyErr_Clear(in.tls)
			return ""
		}
		in.formatExc = in.getAttr(mod, "format_exception")
		libpython.XPy_DecRef(in.tls, mod)
		if in.formatExc == 0 {
			libpython.XPyErr_Clear(in.tls)
			return ""
		}
	}
	lines := libpython.XPyObject_CallOneArg(in.tls, in.formatExc, exc)
	if lines == 0 {
		libpython.XPyErr_Clear(in.tls)
		return ""
	}
	defer libpython.XPy_DecRef(in.tls, lines)

	n := libpython.XPyObject_Length(in.tls, lines)
	var b strings.Builder
	for i := int64(0); i < n; i++ {
		item := libpython.XPyList_GetItem(in.tls, lines, i)
		if item == 0 {
			libpython.XPyErr_Clear(in.tls)
			break
		}
		b.WriteString(in.rawStr(item))
	}
	return b.String()
}

// typeName returns type(o).__name__, prefixed with the defining module when
// that is neither builtins nor __main__.
func (in *Interpreter) typeName(o uintptr) string {
	typ := libpython.XPyObject_Type(in.tls, o)
	if typ == 0 {
		libpython.XPyErr_Clear(in.tls)
		return "?"
	}
	defer libpython.XPy_DecRef(in.tls, typ)

	name := "?"
	if p := in.getAttr(typ, "__name__"); p != 0 {
		name = in.rawStr(p)
		libpython.XPy_DecRef(in.tls, p)
	} else {
		libpython.XPyErr_Clear(in.tls)
	}
	if p := in.getAttr(typ, "__module__"); p != 0 {
		mod := in.rawStr(p)
		libpython.XPy_DecRef(in.tls, p)
		if mod != "" && mod != "builtins" && mod != "__main__" {
			name = mod + "." + name
		}
	} else {
		libpython.XPyErr_Clear(in.tls)
	}
	return name
}

// rawStr is str(o) with every failure swallowed. It never raises and never
// recurses into pyError.
func (in *Interpreter) rawStr(o uintptr) string {
	s := libpython.XPyObject_Str(in.tls, o)
	if s == 0 {
		libpython.XPyErr_Clear(in.tls)
		return ""
	}
	defer libpython.XPy_DecRef(in.tls, s)
	p := libpython.XPyUnicode_AsUTF8AndSize(in.tls, s, in.scratch)
	if p == 0 {
		libpython.XPyErr_Clear(in.tls)
		return ""
	}
	n := *(*int64)(up(in.scratch))
	return string(unsafe.Slice((*byte)(up(p)), n))
}

// raise sets a Python exception describing err and returns NULL, for use by
// the host function trampoline. An *Error carrying its original exception
// object is re-raised unchanged.
func (in *Interpreter) raise(err error) uintptr {
	var pe *Error
	if errors.As(err, &pe) && pe.Exception != nil {
		if p := pe.Exception.p.Load(); p != 0 {
			libpython.XPyErr_SetRaisedException(in.tls, libpython.XPy_NewRef(in.tls, p))
			return 0
		}
	}
	var xe *ExitError
	if errors.As(err, &xe) {
		code := libpython.XPyLong_FromLongLong(in.tls, int64(xe.Code))
		libpython.XPyErr_SetObject(in.tls, libpython.XPyExc_SystemExit, code)
		libpython.XPy_DecRef(in.tls, code)
		return 0
	}
	msg, _ := libc.CString(err.Error())
	libpython.XPyErr_SetString(in.tls, libpython.XPyExc_RuntimeError, msg)
	libc.Xfree(in.tls, msg)
	return 0
}
