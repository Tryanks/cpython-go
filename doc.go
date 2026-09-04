// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

/*
Package cpython embeds CPython in a Go program without cgo.

The interpreter is the ccgo transpilation of the CPython C sources in
[github.com/Tryanks/cpython-go/libpython]; the standard library is embedded in
the binary by [github.com/Tryanks/cpython-go/stdlib]. Nothing has to be
installed on the machine running the program.

# Basic use

	in, err := cpython.New()
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(context.Background(), "x = sum(range(10))"); err != nil {
		log.Fatal(err)
	}
	x, err := in.Get("x")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(x.Int()) // 45 <nil>

# Calling Go from Python

[Interpreter.Func] wraps an ordinary Go function; [Interpreter.NewFunc] takes
the untyped [HostFunc] form when the signature is dynamic.

	in.Set("greet", func(name string) string { return "hello " + name })
	in.Exec(ctx, "print(greet('world'))")

# Value conversion

Go to Python, used by [Interpreter.Object], [Interpreter.Set],
[Object.SetAttr], [Object.SetItem] and the arguments of [Object.Call]:

	nil, a nil pointer or a nil interface  None
	bool                                   bool
	int, int8 ... int64                    int
	uint, uint8 ... uint64, uintptr        int
	float32, float64                       float
	string                                 str
	[]byte                                 bytes
	*big.Int                               int
	*Object                                the same object
	any slice or array                     list
	any map                                dict, keys converted too
	any func                               a host function, see Func
	anything else                          error wrapping ErrUnsupported

Python to Go, used by [Object.Value] and by the parameters of a function
registered with [Interpreter.Func]:

	None                     nil
	bool                     bool
	int                      int64, or *big.Int if it does not fit
	float                    float64
	str                      string
	bytes, bytearray         []byte
	list, tuple              []any, recursively
	set, frozenset           []any, recursively
	dict                     map[string]any if every key is a str,
	                         else map[any]any; unhashable keys become
	                         their repr
	anything else            *Object, holding a reference

# Errors

A Python exception becomes an [*Error] with the type name, the message and a
formatted traceback. SystemExit, raised by sys.exit, becomes an [*ExitError]
carrying the exit status, as does a call to exit, _exit or abort by the C
code. A Go panic escaping the transpiled interpreter becomes a [*CrashError];
the interpreter is then poisoned and every later call returns [ErrCrashed].

Recovering a panic keeps the process alive but cannot repair the C heap. For
real isolation, run the cpython-go command as a subprocess instead.

# Concurrency and threading

The transpiled runtime has no thread local storage, so the whole interpreter
runs on one libc.TLS. Every method serializes on a mutex, and which goroutine
makes the call does not matter. Python level threading (the threading module,
concurrent.futures thread pools) is not supported.

While one of your host functions is running, the goroutine executing it may
call back into the same Interpreter; that is what makes [Object.Attr] and
friends usable from a [HostFunc]. Calling the Interpreter from a *different*
goroutine during that window is a programming error and is not detected. The
one exception is [Interpreter.Interrupt], which is designed to be called from
another goroutine, and which context cancellation uses to stop long running
code with a KeyboardInterrupt.

Only one Interpreter can be open per process, because CPython keeps its state
in globals. [New] returns [ErrAlreadyOpen] otherwise. [Interpreter.Close]
calls Py_FinalizeEx and allows a later New; CPython does not give back
everything it allocated, so repeated cycles leak.
*/
package cpython
