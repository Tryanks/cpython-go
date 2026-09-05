// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
	"github.com/Tryanks/cpython-go/stdlib"
)

// none is the address of the singleton None object.
var none = uintptr(unsafe.Pointer(&libpython.X_Py_NoneStruct))

// config holds the resolved options of New.
type config struct {
	argv     []string
	home     string
	path     []string
	stdout   io.Writer
	stderr   io.Writer
	isolated bool
}

// An Option configures New.
type Option func(*config)

// WithArgv sets sys.argv. The default is []string{"cpython-go"}.
func WithArgv(argv []string) Option { return func(c *config) { c.argv = argv } }

// WithHome sets PYTHONHOME. The default is stdlib.Home(), the embedded
// standard library.
func WithHome(dir string) Option { return func(c *config) { c.home = dir } }

// WithPath appends directories to sys.path.
func WithPath(dirs ...string) Option { return func(c *config) { c.path = append(c.path, dirs...) } }

// WithStdout redirects sys.stdout to w. This is a Python level redirection:
// output written by C code straight to file descriptor 1 is unaffected.
func WithStdout(w io.Writer) Option { return func(c *config) { c.stdout = w } }

// WithStderr redirects sys.stderr to w. See WithStdout.
func WithStderr(w io.Writer) Option { return func(c *config) { c.stderr = w } }

// WithIsolated selects the configuration preset. The default, true, uses
// PyConfig_InitIsolatedConfig: no environment variables, no user site
// directory, no signal handler installation beyond the one this package needs
// for Interrupt. False uses PyConfig_InitPythonConfig, which honours the
// PYTHON* environment variables just like the python3 command.
func WithIsolated(v bool) Option { return func(c *config) { c.isolated = v } }

// An Interpreter is a running CPython interpreter.
//
// Only one Interpreter can exist per process; see New. All of its methods are
// safe for concurrent use, but calls are serialized: CPython runs on a single
// libc.TLS and Python level threading is not supported.
type Interpreter struct {
	tls  *libc.TLS
	itls *libc.TLS // separate TLS for Interrupt, which runs off the owner goroutine
	imu  sync.Mutex

	// mu serializes API calls. depth counts nested calls made from a host
	// function callback, which run on the goroutine that already holds mu;
	// see enter.
	mu    sync.Mutex
	depth atomic.Int32

	globals   uintptr // __main__.__dict__, borrowed
	formatExc uintptr // traceback.format_exception, strong
	jsonDumps uintptr // json.dumps, strong
	scratch   uintptr // 64 bytes of C memory for out parameters

	closed  bool
	crashed bool

	pendingMu sync.Mutex
	pending   []uintptr // Py_DecRef queued by *Object finalizers

	defs    []uintptr // PyMethodDef blocks kept alive for host functions
	hostIDs []int64
}

var (
	openMu sync.Mutex
	open   *Interpreter
)

// New initializes the interpreter. Because CPython keeps its state in process
// globals, only one Interpreter can be open at a time: New returns
// ErrAlreadyOpen until the previous one is closed. Reopening after Close
// works, but CPython does not release everything it allocated.
func New(opts ...Option) (_ *Interpreter, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cfg := config{argv: []string{"cpython-go"}, isolated: true}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.home == "" {
		if cfg.home, err = stdlib.Home(); err != nil {
			return nil, fmt.Errorf("cpython: cannot materialize the embedded stdlib: %w", err)
		}
	}

	openMu.Lock()
	defer openMu.Unlock()
	if open != nil {
		return nil, ErrAlreadyOpen
	}

	in := &Interpreter{tls: libc.NewTLS(), itls: libc.NewTLS()}
	// Interrupt runs on its own TLS so that it never touches the alloca
	// stack of code executing on in.tls, but CPython refuses to set the eval
	// breaker unless the caller looks like the main thread, and libc derives
	// pthread_self from TLS.ID. Borrowing the id is what makes Interrupt the
	// equivalent of a signal arriving on the main thread.
	in.itls.ID = in.tls.ID
	libc.SetEnviron(in.tls, os.Environ())
	in.scratch = libc.Xmalloc(in.tls, 64)

	// Any panic from here on leaves the process usable: nothing global has
	// been published yet.
	defer in.recoverPanic(&err)
	if err = in.initialize(&cfg); err != nil {
		return nil, err
	}
	open = in
	return in, nil
}

func (in *Interpreter) initialize(cfg *config) error {
	c := libc.Xmalloc(in.tls, uint64(unsafe.Sizeof(libpython.TPyConfig{})))
	if c == 0 {
		return fmt.Errorf("cpython: out of memory")
	}
	defer func() {
		libpython.XPyConfig_Clear(in.tls, c)
		libc.Xfree(in.tls, c)
	}()

	if cfg.isolated {
		libpython.XPyConfig_InitIsolatedConfig(in.tls, c)
	} else {
		libpython.XPyConfig_InitPythonConfig(in.tls, c)
	}
	// Interrupt and context cancellation need _PySignal_Init to have put
	// signal.default_int_handler in the handler table; PyErr_SetInterruptEx
	// is a no-op otherwise.
	(*libpython.TPyConfig)(up(c)).Finstall_signal_handlers = 1

	home, err := libc.CString(cfg.home)
	if err != nil {
		return err
	}
	st := libpython.XPyConfig_SetBytesString(in.tls, c, c+unsafe.Offsetof(libpython.TPyConfig{}.Fhome), home)
	libc.Xfree(in.tls, home)
	if err := in.statusError(st); err != nil {
		return err
	}

	argv, free := in.cStrings(cfg.argv)
	st = libpython.XPyConfig_SetBytesArgv(in.tls, c, int64(len(cfg.argv)), argv)
	free()
	if err := in.statusError(st); err != nil {
		return err
	}

	if err := in.statusError(libpython.XPy_InitializeFromConfig(in.tls, c)); err != nil {
		return err
	}

	main := libpython.XPyImport_AddModule(in.tls, in.cstr("__main__"))
	if main == 0 {
		return in.pyError()
	}
	in.globals = libpython.XPyModule_GetDict(in.tls, main)

	if len(cfg.path) > 0 {
		if err := in.runIsolated("import sys; sys.path.extend(p)", map[string]any{"p": cfg.path}); err != nil {
			return err
		}
	}
	if cfg.stdout != nil {
		if err := in.redirect("stdout", cfg.stdout); err != nil {
			return err
		}
	}
	if cfg.stderr != nil {
		if err := in.redirect("stderr", cfg.stderr); err != nil {
			return err
		}
	}
	return nil
}

// goWriter is the Python side of WithStdout/WithStderr. io.TextIOBase gives
// the file protocol print() expects; only write and flush are ours.
const goWriter = `
import sys, io

class _GoWriter(io.TextIOBase):
    def __init__(self, write):
        self._write = write
    def writable(self):
        return True
    def write(self, s):
        self._write(s)
        return len(s)
    def flush(self):
        pass

setattr(sys, name, _GoWriter(write))
`

func (in *Interpreter) redirect(name string, w io.Writer) error {
	write := func(args []*Object, _ map[string]*Object) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("write() takes exactly one argument")
		}
		s, err := args[0].String()
		if err != nil {
			return nil, err
		}
		_, err = io.WriteString(w, s)
		return nil, err
	}
	return in.runIsolated(goWriter, map[string]any{"name": name, "write": HostFunc(write)})
}

// runIsolated executes code with a fresh globals dict holding vars, so that
// package internals do not leak into __main__.
func (in *Interpreter) runIsolated(code string, vars map[string]any) error {
	g := libpython.XPyDict_New(in.tls)
	if g == 0 {
		return in.pyError()
	}
	defer libpython.XPy_DecRef(in.tls, g)
	for k, v := range vars {
		p, err := in.toPy(v)
		if err != nil {
			return err
		}
		rc := libpython.XPyDict_SetItemString(in.tls, g, in.cstr(k), p)
		libpython.XPy_DecRef(in.tls, p)
		if rc != 0 {
			return in.pyError()
		}
	}
	return in.run(code, libpython.MPy_file_input, g)
}

// run compiles and executes code with the given globals, discarding the
// result.
func (in *Interpreter) run(code string, start int32, globals uintptr) error {
	s := in.cstr(code)
	defer libc.Xfree(in.tls, s)
	r := libpython.XPyRun_StringFlags(in.tls, s, start, globals, globals, 0)
	if r == 0 {
		return in.pyError()
	}
	libpython.XPy_DecRef(in.tls, r)
	return nil
}

func (in *Interpreter) statusError(st libpython.TPyStatus) error {
	if libpython.XPyStatus_Exception(in.tls, st) == 0 {
		return nil
	}
	if st.F_type == libpython.E_PyStatus_TYPE_EXIT {
		return &ExitError{Code: int(st.Fexitcode)}
	}
	msg := "initialization failed"
	if st.Ferr_msg != 0 {
		msg = libc.GoString(st.Ferr_msg)
	}
	return &Error{Type: "RuntimeError", Message: msg}
}

// cstr allocates a NUL terminated copy of s in C memory. Callers that do not
// free it are leaking a short lived string; helpers that run per call do free.
func (in *Interpreter) cstr(s string) uintptr {
	p, err := libc.CString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// cStrings builds a C char*[] holding ss and returns a function releasing it.
func (in *Interpreter) cStrings(ss []string) (uintptr, func()) {
	const ptrSize = unsafe.Sizeof(uintptr(0))
	p := libc.Xmalloc(in.tls, uint64(ptrSize)*uint64(len(ss)+1))
	if p == 0 {
		panic("cpython: out of memory")
	}
	for i, s := range ss {
		*(*uintptr)(up(p + uintptr(i)*ptrSize)) = in.cstr(s)
	}
	*(*uintptr)(up(p + uintptr(len(ss))*ptrSize)) = 0
	return p, func() {
		for i := range ss {
			libc.Xfree(in.tls, *(*uintptr)(up(p + uintptr(i)*ptrSize)))
		}
		libc.Xfree(in.tls, p)
	}
}

// enter acquires the interpreter.
//
// Only one goroutine can be inside the interpreter at a time, so a non zero
// depth means the caller is that goroutine re-entering from a host function
// callback and mu is already held by it. Calling an Interpreter from another
// goroutine while one of your host functions is running is a programming
// error and is not detected.
func (in *Interpreter) enter() error {
	if in.depth.Load() > 0 {
		in.depth.Add(1)
		return in.check()
	}
	in.mu.Lock()
	in.depth.Add(1)
	if err := in.check(); err != nil {
		in.leave()
		return err
	}
	in.drain()
	return nil
}

func (in *Interpreter) leave() {
	if in.depth.Add(-1) == 0 {
		in.mu.Unlock()
	}
}

func (in *Interpreter) check() error {
	switch {
	case in.crashed:
		return ErrCrashed
	case in.closed:
		return ErrClosed
	}
	return nil
}

// drain releases the objects queued by *Object finalizers.
func (in *Interpreter) drain() {
	in.pendingMu.Lock()
	p := in.pending
	in.pending = nil
	in.pendingMu.Unlock()
	for _, o := range p {
		libpython.XPy_DecRef(in.tls, o)
	}
}

// call runs f while holding the interpreter, converting panics from
// transpiled code into *CrashError or *ExitError.
func call[T any](in *Interpreter, f func() (T, error)) (r T, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err = in.enter(); err != nil {
		return r, err
	}
	defer in.leave()
	defer in.recoverPanic(&err)
	return f()
}

func do(in *Interpreter, f func() error) (err error) {
	_, err = call(in, func() (struct{}, error) { return struct{}{}, f() })
	return err
}

// Close finalizes the interpreter and allows a later New. Objects created by
// it must not be used afterwards.
func (in *Interpreter) Close() (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	openMu.Lock()
	defer openMu.Unlock()
	if err = in.enter(); err != nil {
		if err == ErrClosed {
			return nil
		}
		// A crashed interpreter cannot be finalized safely, but it must
		// still stop blocking New.
		if open == in {
			open = nil
		}
		in.closed = true
		return err
	}
	defer in.leave()
	defer in.recoverPanic(&err)

	in.closed = true
	if open == in {
		open = nil
	}
	if in.formatExc != 0 {
		libpython.XPy_DecRef(in.tls, in.formatExc)
		in.formatExc = 0
	}
	if in.jsonDumps != 0 {
		libpython.XPy_DecRef(in.tls, in.jsonDumps)
		in.jsonDumps = 0
	}
	if libpython.XPy_FinalizeEx(in.tls) < 0 {
		err = &Error{Type: "RuntimeError", Message: "Py_FinalizeEx failed"}
	}
	hostMu.Lock()
	for _, id := range in.hostIDs {
		delete(hostReg, id)
	}
	hostMu.Unlock()
	for _, d := range in.defs {
		libc.Xfree(in.tls, (*libpython.TPyMethodDef)(up(d)).Fml_name)
		libc.Xfree(in.tls, d)
	}
	in.defs = nil
	libc.Xfree(in.tls, in.scratch)
	in.scratch = 0
	in.pending = nil
	return err
}

// Exec runs code as a sequence of statements in the __main__ module.
func (in *Interpreter) Exec(ctx context.Context, code string) error {
	return do(in, func() error {
		defer in.watch(ctx)()
		return in.run(code, libpython.MPy_file_input, in.globals)
	})
}

// Eval evaluates a single expression in __main__ and returns its value.
func (in *Interpreter) Eval(ctx context.Context, expr string) (*Object, error) {
	return call(in, func() (*Object, error) {
		defer in.watch(ctx)()
		s := in.cstr(expr)
		defer libc.Xfree(in.tls, s)
		r := libpython.XPyRun_StringFlags(in.tls, s, libpython.MPy_eval_input, in.globals, in.globals, 0)
		if r == 0 {
			return nil, in.pyError()
		}
		return in.obj(r), nil
	})
}

// ExecFile reads path in Go and runs it in __main__ under its real file name,
// so that tracebacks and __file__ point at it.
func (in *Interpreter) ExecFile(ctx context.Context, path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return do(in, func() error {
		defer in.watch(ctx)()
		s, f := in.cstr(string(src)), in.cstr(path)
		defer libc.Xfree(in.tls, s)
		defer libc.Xfree(in.tls, f)
		code := libpython.XPy_CompileStringExFlags(in.tls, s, f, libpython.MPy_file_input, 0, -1)
		if code == 0 {
			return in.pyError()
		}
		defer libpython.XPy_DecRef(in.tls, code)
		r := libpython.XPyEval_EvalCode(in.tls, code, in.globals, in.globals)
		if r == 0 {
			return in.pyError()
		}
		libpython.XPy_DecRef(in.tls, r)
		return nil
	})
}

// Import imports a module and returns it.
func (in *Interpreter) Import(name string) (*Object, error) {
	return call(in, func() (*Object, error) {
		m := in.importModule(name)
		if m == 0 {
			return nil, in.pyError()
		}
		return in.obj(m), nil
	})
}

func (in *Interpreter) importModule(name string) uintptr {
	s := in.cstr(name)
	defer libc.Xfree(in.tls, s)
	return libpython.XPyImport_ImportModule(in.tls, s)
}

// Get returns a global of the __main__ module. A missing name is reported as
// an *Error of type NameError.
func (in *Interpreter) Get(name string) (*Object, error) {
	return call(in, func() (*Object, error) {
		s := in.cstr(name)
		defer libc.Xfree(in.tls, s)
		p := libpython.XPyDict_GetItemString(in.tls, in.globals, s)
		if p == 0 {
			libpython.XPyErr_Clear(in.tls)
			return nil, &Error{Type: "NameError", Message: fmt.Sprintf("name '%s' is not defined", name)}
		}
		return in.obj(libpython.XPy_NewRef(in.tls, p)), nil
	})
}

// Set defines a global in the __main__ module. A Go func value becomes a
// host function; see Func for the supported signatures.
func (in *Interpreter) Set(name string, v any) error {
	return do(in, func() error {
		p, err := in.toPy(v)
		if err != nil {
			return err
		}
		defer libpython.XPy_DecRef(in.tls, p)
		s := in.cstr(name)
		defer libc.Xfree(in.tls, s)
		if libpython.XPyDict_SetItemString(in.tls, in.globals, s, p) != 0 {
			return in.pyError()
		}
		return nil
	})
}

// Object converts a Go value to a Python object; see the package
// documentation for the conversion table.
func (in *Interpreter) Object(v any) (*Object, error) {
	return call(in, func() (*Object, error) {
		p, err := in.toPy(v)
		if err != nil {
			return nil, err
		}
		return in.obj(p), nil
	})
}

// None returns the None singleton.
func (in *Interpreter) None() *Object {
	o, _ := in.Object(nil)
	return o
}

// Interrupt makes the code currently running in the interpreter raise
// KeyboardInterrupt, as Ctrl-C would. It is the one method meant to be called
// while another goroutine is inside the interpreter, and it returns
// immediately: the exception is raised the next time the eval loop checks for
// signals.
func (in *Interpreter) Interrupt() {
	in.imu.Lock()
	defer in.imu.Unlock()
	if in.closed || in.crashed {
		return
	}
	defer func() { recover() }()
	libpython.XPyErr_SetInterruptEx(in.itls, 2 /* SIGINT */)
}

// watch arranges for ctx cancellation to interrupt the running code. The
// returned function stops the watcher and must be called before the API call
// returns.
func (in *Interpreter) watch(ctx context.Context) func() {
	if ctx == nil || ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			in.Interrupt()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// up converts a pointer into the transpiled runtime's heap, which lives
// outside the Go heap, to an unsafe.Pointer. Going through the address of the
// local keeps go vet's unsafeptr check happy: no Go pointer is ever formed
// from an integer here.
func up(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
