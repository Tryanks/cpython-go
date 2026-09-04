// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
)

// A HostFunc is a Go function callable from Python. The arguments are
// borrowed for the duration of the call; retain one by keeping the *Object
// alive. The result is converted by the rules of Interpreter.Object; a
// returned error becomes a Python RuntimeError, except for an *Error, which
// re-raises its original exception, and an *ExitError, which raises
// SystemExit.
type HostFunc func(args []*Object, kwargs map[string]*Object) (any, error)

// The registry maps the id carried by a PyCFunction's self argument back to
// the Go closure, so that a single trampoline with the transpiled calling
// convention serves every host function.
var (
	hostMu   sync.Mutex
	hostReg  = map[int64]*hostEntry{}
	hostNext int64
)

type hostEntry struct {
	in *Interpreter
	fn HostFunc
}

// NewFunc wraps fn as a Python callable. name appears in tracebacks.
func (in *Interpreter) NewFunc(name string, fn HostFunc) (*Object, error) {
	return call(in, func() (*Object, error) {
		p, err := in.hostFunc(name, fn)
		if err != nil {
			return nil, err
		}
		return in.obj(p), nil
	})
}

// hostFunc builds the PyCFunction. It must run with the interpreter held.
func (in *Interpreter) hostFunc(name string, fn HostFunc) (uintptr, error) {
	hostMu.Lock()
	hostNext++
	id := hostNext
	hostReg[id] = &hostEntry{in: in, fn: fn}
	hostMu.Unlock()
	in.hostIDs = append(in.hostIDs, id)

	// The PyMethodDef must outlive the callable, so it lives in C memory
	// until Close.
	def := libc.Xmalloc(in.tls, uint64(unsafe.Sizeof(libpython.TPyMethodDef{})))
	if def == 0 {
		return 0, fmt.Errorf("cpython: out of memory")
	}
	in.defs = append(in.defs, def)
	d := (*libpython.TPyMethodDef)(up(def))
	d.Fml_name = in.cstr(name)
	d.Fml_meth = fp(hostTrampoline)
	d.Fml_flags = libpython.MMETH_VARARGS | libpython.MMETH_KEYWORDS
	d.Fml_doc = 0

	self, err := in.fromInt64(id)
	if err != nil {
		return 0, err
	}
	defer libpython.XPy_DecRef(in.tls, self)
	return in.check0(libpython.XPyCFunction_NewEx(in.tls, def, self, 0))
}

// fp extracts the code pointer of a top level Go function so that transpiled
// code can call it, the way the generated __ccgo_fp does. It must not be used
// on closures: nothing would keep them reachable.
func fp(f any) uintptr {
	type iface [2]uintptr
	return (*iface)(unsafe.Pointer(&f))[1]
}

// hostTrampoline has the METH_VARARGS|METH_KEYWORDS calling convention. self
// is the int identifying the Go closure in hostReg.
func hostTrampoline(tls *libc.TLS, self, args, kwargs uintptr) uintptr {
	id := libpython.XPyLong_AsLong(tls, self)
	hostMu.Lock()
	e := hostReg[id]
	hostMu.Unlock()
	if e == nil {
		msg, _ := libc.CString("cpython: host function is no longer registered")
		libpython.XPyErr_SetString(tls, libpython.XPyExc_RuntimeError, msg)
		libc.Xfree(tls, msg)
		return 0
	}
	return e.in.callHost(e.fn, args, kwargs)
}

func (in *Interpreter) callHost(fn HostFunc, args, kwargs uintptr) (r uintptr) {
	// A panic must not unwind through the C frames below us; it becomes a
	// Python exception instead.
	defer func() {
		if v := recover(); v != nil {
			if e, ok := v.(interface{ ExitCode() int }); ok {
				r = in.raise(&ExitError{Code: e.ExitCode()})
				return
			}
			r = in.raise(fmt.Errorf("cpython: panic in host function: %v", v))
		}
	}()

	var goArgs []*Object
	if args != 0 {
		n := libpython.XPyObject_Length(in.tls, args)
		goArgs = make([]*Object, n)
		for i := int64(0); i < n; i++ {
			goArgs[i] = in.obj(libpython.XPy_NewRef(in.tls, libpython.XPyTuple_GetItem(in.tls, args, i)))
		}
	}
	var goKw map[string]*Object
	if kwargs != 0 {
		keys, err := in.slice(kwargs)
		if err != nil {
			return in.raise(err)
		}
		goKw = make(map[string]*Object, len(keys))
		for _, k := range keys {
			name, err := k.String()
			if err != nil {
				return in.raise(err)
			}
			v := libpython.XPyObject_GetItem(in.tls, kwargs, k.p.Load())
			if v == 0 {
				return in.raise(in.pyError())
			}
			goKw[name] = in.obj(v)
		}
	}

	res, err := fn(goArgs, goKw)
	if err != nil {
		return in.raise(err)
	}
	p, err := in.toPy(res)
	if err != nil {
		return in.raise(err)
	}
	return p
}

// Func wraps an ordinary Go function by reflection.
//
// Parameters are converted from Python by the rules of Object.Value and must
// be one of bool, the int, uint and float kinds, string, []byte, []any,
// map[string]any, any or *Object; variadic functions are supported. The
// results must be (), (T), (error) or (T, error).
func (in *Interpreter) Func(name string, fn any) (*Object, error) {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return nil, fmt.Errorf("%w: %T is not a function", ErrUnsupported, fn)
	}
	h, err := in.reflectHost(name, rv)
	if err != nil {
		return nil, err
	}
	return in.NewFunc(name, h)
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

func (in *Interpreter) reflectHost(name string, rv reflect.Value) (HostFunc, error) {
	t := rv.Type()
	switch t.NumOut() {
	case 0, 1:
	case 2:
		if t.Out(1) != errorType {
			return nil, fmt.Errorf("%w: %s must return (T), (T, error), (error) or nothing", ErrUnsupported, name)
		}
	default:
		return nil, fmt.Errorf("%w: %s returns %d values", ErrUnsupported, name, t.NumOut())
	}

	return func(args []*Object, kwargs map[string]*Object) (any, error) {
		if len(kwargs) != 0 {
			return nil, fmt.Errorf("%s() takes no keyword arguments", name)
		}
		nfixed := t.NumIn()
		if t.IsVariadic() {
			nfixed--
			if len(args) < nfixed {
				return nil, fmt.Errorf("%s() takes at least %d arguments (%d given)", name, nfixed, len(args))
			}
		} else if len(args) != nfixed {
			return nil, fmt.Errorf("%s() takes exactly %d arguments (%d given)", name, nfixed, len(args))
		}

		argv := make([]reflect.Value, len(args))
		for i, a := range args {
			et := t.In(min(i, t.NumIn()-1))
			if t.IsVariadic() && i >= nfixed {
				et = et.Elem()
			}
			v, err := convertTo(a, et)
			if err != nil {
				return nil, fmt.Errorf("%s() argument %d: %w", name, i+1, err)
			}
			argv[i] = v
		}

		out := rv.Call(argv)
		if len(out) > 0 && t.Out(len(out)-1) == errorType {
			if e, _ := out[len(out)-1].Interface().(error); e != nil {
				return nil, e
			}
			out = out[:len(out)-1]
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out[0].Interface(), nil
	}, nil
}

var objectType = reflect.TypeOf((*Object)(nil))

// convertTo converts a Python argument to the Go type t.
func convertTo(o *Object, t reflect.Type) (reflect.Value, error) {
	if t == objectType {
		return reflect.ValueOf(o), nil
	}
	v, err := o.Value()
	if err != nil {
		return reflect.Value{}, err
	}
	if t.Kind() == reflect.Interface && t.NumMethod() == 0 {
		if v == nil {
			return reflect.Zero(t), nil
		}
		return reflect.ValueOf(v), nil
	}
	if v == nil {
		return reflect.Zero(t), nil
	}
	rv := reflect.ValueOf(v)
	switch {
	case rv.Type() == t:
		return rv, nil
	case rv.CanConvert(t) && numeric(rv.Kind()) == numeric(t.Kind()):
		return rv.Convert(t), nil
	}
	return reflect.Value{}, fmt.Errorf("%w: cannot use %s as %s", ErrUnsupported, o.Type(), t)
}

// numeric reports whether k is one of the kinds convertTo is willing to
// widen or narrow, which keeps reflect from turning an int64 into a string.
func numeric(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
