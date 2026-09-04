// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"fmt"
	"math/big"
	"runtime"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
)

// An Object is a strong reference to a Python object.
//
// Release drops the reference deterministically. An Object that becomes
// unreachable without being released is queued by a finalizer and released at
// the next call into its Interpreter, so leaking one only delays the free.
type Object struct {
	in *Interpreter
	p  atomic.Uintptr
}

// obj takes ownership of a strong reference and wraps it.
func (in *Interpreter) obj(p uintptr) *Object {
	o := &Object{in: in}
	o.p.Store(p)
	runtime.SetFinalizer(o, (*Object).finalize)
	return o
}

func (o *Object) finalize() {
	p := o.p.Swap(0)
	if p == 0 {
		return
	}
	o.in.pendingMu.Lock()
	o.in.pending = append(o.in.pending, p)
	o.in.pendingMu.Unlock()
}

// Release drops the reference now. It is idempotent, and further use of the
// Object reports an error.
func (o *Object) Release() {
	p := o.p.Swap(0)
	if p == 0 {
		return
	}
	runtime.SetFinalizer(o, nil)
	if err := o.in.enter(); err != nil {
		return // closed or crashed: nothing safe left to do
	}
	defer o.in.leave()
	defer func() { recover() }()
	libpython.XPy_DecRef(o.in.tls, p)
}

// Interpreter returns the interpreter that owns o.
func (o *Object) Interpreter() *Interpreter { return o.in }

var errReleased = fmt.Errorf("cpython: object has been released")

// ptr returns the borrowed pointer, or an error if o was released.
func (o *Object) ptr() (uintptr, error) {
	p := o.p.Load()
	if p == 0 {
		return 0, errReleased
	}
	return p, nil
}

// isa reports whether o is an instance of the given static type, without
// running any Python code.
func (in *Interpreter) isa(o uintptr, t *libpython.TPyTypeObject) bool {
	ot := (*libpython.TPyObject)(up(o)).Fob_type
	tp := uintptr(unsafe.Pointer(t))
	return ot == tp || libpython.XPyType_IsSubtype(in.tls, ot, tp) != 0
}

// getAttr is PyObject_GetAttrString; it returns 0 with the exception set.
func (in *Interpreter) getAttr(o uintptr, name string) uintptr {
	s := in.cstr(name)
	defer libc.Xfree(in.tls, s)
	return libpython.XPyObject_GetAttrString(in.tls, o, s)
}

// Attr returns the named attribute of o.
func (o *Object) Attr(name string) (*Object, error) {
	return call(o.in, func() (*Object, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		r := o.in.getAttr(p, name)
		if r == 0 {
			return nil, o.in.pyError()
		}
		return o.in.obj(r), nil
	})
}

// SetAttr sets the named attribute of o to v, converted by the rules of
// Interpreter.Object.
func (o *Object) SetAttr(name string, v any) error {
	return do(o.in, func() error {
		p, err := o.ptr()
		if err != nil {
			return err
		}
		vp, err := o.in.toPy(v)
		if err != nil {
			return err
		}
		defer libpython.XPy_DecRef(o.in.tls, vp)
		s := o.in.cstr(name)
		defer libc.Xfree(o.in.tls, s)
		if libpython.XPyObject_SetAttrString(o.in.tls, p, s, vp) != 0 {
			return o.in.pyError()
		}
		return nil
	})
}

// Item is o[key].
func (o *Object) Item(key any) (*Object, error) {
	return call(o.in, func() (*Object, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		kp, err := o.in.toPy(key)
		if err != nil {
			return nil, err
		}
		defer libpython.XPy_DecRef(o.in.tls, kp)
		r := libpython.XPyObject_GetItem(o.in.tls, p, kp)
		if r == 0 {
			return nil, o.in.pyError()
		}
		return o.in.obj(r), nil
	})
}

// SetItem is o[key] = v.
func (o *Object) SetItem(key, v any) error {
	return do(o.in, func() error {
		p, err := o.ptr()
		if err != nil {
			return err
		}
		kp, err := o.in.toPy(key)
		if err != nil {
			return err
		}
		defer libpython.XPy_DecRef(o.in.tls, kp)
		vp, err := o.in.toPy(v)
		if err != nil {
			return err
		}
		defer libpython.XPy_DecRef(o.in.tls, vp)
		if libpython.XPyObject_SetItem(o.in.tls, p, kp, vp) != 0 {
			return o.in.pyError()
		}
		return nil
	})
}

// Call calls o with the given positional arguments.
func (o *Object) Call(args ...any) (*Object, error) { return o.CallKw(args, nil) }

// CallKw calls o with positional and keyword arguments.
func (o *Object) CallKw(args []any, kwargs map[string]any) (*Object, error) {
	return call(o.in, func() (*Object, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		in := o.in
		tuple := libpython.XPyTuple_New(in.tls, int64(len(args)))
		if tuple == 0 {
			return nil, in.pyError()
		}
		defer libpython.XPy_DecRef(in.tls, tuple)
		for i, a := range args {
			ap, err := in.toPy(a)
			if err != nil {
				return nil, err
			}
			libpython.XPyTuple_SetItem(in.tls, tuple, int64(i), ap) // steals ap
		}
		var kw uintptr
		if kwargs != nil {
			if kw, err = in.toPy(kwargs); err != nil {
				return nil, err
			}
			defer libpython.XPy_DecRef(in.tls, kw)
		}
		r := libpython.XPyObject_Call(in.tls, p, tuple, kw)
		if r == 0 {
			return nil, in.pyError()
		}
		return in.obj(r), nil
	})
}

// Len is len(o).
func (o *Object) Len() (int, error) {
	return call(o.in, func() (int, error) {
		p, err := o.ptr()
		if err != nil {
			return 0, err
		}
		n := libpython.XPyObject_Length(o.in.tls, p)
		if n < 0 {
			return 0, o.in.pyError()
		}
		return int(n), nil
	})
}

// Slice materializes any iterable as a Go slice of new references.
func (o *Object) Slice() ([]*Object, error) {
	return call(o.in, func() ([]*Object, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		return o.in.slice(p)
	})
}

func (in *Interpreter) slice(p uintptr) ([]*Object, error) {
	list := libpython.XPySequence_List(in.tls, p)
	if list == 0 {
		return nil, in.pyError()
	}
	defer libpython.XPy_DecRef(in.tls, list)
	n := libpython.XPyObject_Length(in.tls, list)
	r := make([]*Object, n)
	for i := int64(0); i < n; i++ {
		item := libpython.XPyList_GetItem(in.tls, list, i)
		if item == 0 {
			return nil, in.pyError()
		}
		r[i] = in.obj(libpython.XPy_NewRef(in.tls, item))
	}
	return r, nil
}

// Type returns type(o).__name__, qualified with the defining module when that
// is neither builtins nor __main__.
func (o *Object) Type() string {
	s, _ := call(o.in, func() (string, error) {
		p, err := o.ptr()
		if err != nil {
			return "?", nil
		}
		return o.in.typeName(p), nil
	})
	if s == "" {
		return "?"
	}
	return s
}

// IsNone reports whether o is None.
func (o *Object) IsNone() bool { return o.p.Load() == none }

// Bool is bool(o).
func (o *Object) Bool() (bool, error) {
	return call(o.in, func() (bool, error) {
		p, err := o.ptr()
		if err != nil {
			return false, err
		}
		r := libpython.XPyObject_IsTrue(o.in.tls, p)
		if r < 0 {
			return false, o.in.pyError()
		}
		return r != 0, nil
	})
}

// Int returns o as an int64. Values that do not fit report an OverflowError;
// use BigInt for those.
func (o *Object) Int() (int64, error) {
	return call(o.in, func() (int64, error) {
		p, err := o.ptr()
		if err != nil {
			return 0, err
		}
		return o.in.int64(p)
	})
}

func (in *Interpreter) int64(p uintptr) (int64, error) {
	v := libpython.XPyLong_AsLongLongAndOverflow(in.tls, p, in.scratch)
	if v == -1 && libpython.XPyErr_Occurred(in.tls) != 0 {
		return 0, in.pyError()
	}
	if *(*int32)(up(in.scratch)) != 0 {
		return 0, &Error{Type: "OverflowError", Message: "Python int too large to convert to int64"}
	}
	return v, nil
}

// BigInt returns o as an arbitrary precision integer.
func (o *Object) BigInt() (*big.Int, error) {
	return call(o.in, func() (*big.Int, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		return o.in.bigInt(p)
	})
}

func (in *Interpreter) bigInt(p uintptr) (*big.Int, error) {
	n := libpython.XPyNumber_Long(in.tls, p)
	if n == 0 {
		return nil, in.pyError()
	}
	defer libpython.XPy_DecRef(in.tls, n)
	z, ok := new(big.Int).SetString(in.rawStr(n), 10)
	if !ok {
		return nil, &Error{Type: "ValueError", Message: "cannot represent value as an integer"}
	}
	return z, nil
}

// Float returns o as a float64.
func (o *Object) Float() (float64, error) {
	return call(o.in, func() (float64, error) {
		p, err := o.ptr()
		if err != nil {
			return 0, err
		}
		v := libpython.XPyFloat_AsDouble(o.in.tls, p)
		if v == -1 && libpython.XPyErr_Occurred(o.in.tls) != 0 {
			return 0, o.in.pyError()
		}
		return v, nil
	})
}

// String returns the contents of a str object. It fails for anything else;
// use Str for the str() of an arbitrary object.
func (o *Object) String() (string, error) {
	return call(o.in, func() (string, error) {
		p, err := o.ptr()
		if err != nil {
			return "", err
		}
		if !o.in.isa(p, &libpython.XPyUnicode_Type) {
			return "", &Error{Type: "TypeError", Message: "expected str, got " + o.in.typeName(p)}
		}
		return o.in.utf8(p)
	})
}

func (in *Interpreter) utf8(p uintptr) (string, error) {
	s := libpython.XPyUnicode_AsUTF8AndSize(in.tls, p, in.scratch)
	if s == 0 {
		return "", in.pyError()
	}
	n := *(*int64)(up(in.scratch))
	return string(unsafe.Slice((*byte)(up(s)), n)), nil
}

// Bytes returns the contents of a bytes, bytearray or memoryview object.
func (o *Object) Bytes() ([]byte, error) {
	return call(o.in, func() ([]byte, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		return o.in.bytes(p)
	})
}

func (in *Interpreter) bytes(p uintptr) ([]byte, error) {
	src := p
	if !in.isa(p, &libpython.XPyBytes_Type) {
		// bytes(o) copes with bytearray, memoryview and anything else
		// implementing the buffer protocol.
		b := libpython.XPyObject_CallOneArg(in.tls, uintptr(unsafe.Pointer(&libpython.XPyBytes_Type)), p)
		if b == 0 {
			return nil, in.pyError()
		}
		defer libpython.XPy_DecRef(in.tls, b)
		src = b
	}
	if libpython.XPyBytes_AsStringAndSize(in.tls, src, in.scratch, in.scratch+8) != 0 {
		return nil, in.pyError()
	}
	data := *(*uintptr)(up(in.scratch))
	n := *(*int64)(up(in.scratch + 8))
	return []byte(string(unsafe.Slice((*byte)(up(data)), n))), nil
}

// Str is str(o). It never fails; unprintable objects render as
// "<unprintable ...>".
func (o *Object) Str() string { return o.display(libpython.XPyObject_Str) }

// Repr is repr(o). It never fails; see Str.
func (o *Object) Repr() string { return o.display(libpython.XPyObject_Repr) }

func (o *Object) display(f func(*libc.TLS, uintptr) uintptr) string {
	s, _ := call(o.in, func() (string, error) {
		p, err := o.ptr()
		if err != nil {
			return "<unprintable: released>", nil
		}
		r := f(o.in.tls, p)
		if r == 0 {
			libpython.XPyErr_Clear(o.in.tls)
			return "<unprintable " + o.in.typeName(p) + ">", nil
		}
		defer libpython.XPy_DecRef(o.in.tls, r)
		v, err := o.in.utf8(r)
		if err != nil {
			return "<unprintable " + o.in.typeName(p) + ">", nil
		}
		return v, nil
	})
	if s == "" && o.p.Load() == 0 {
		return "<unprintable: released>"
	}
	return s
}

// Value converts o to a Go value; see the package documentation for the
// conversion table.
func (o *Object) Value() (any, error) {
	return call(o.in, func() (any, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		return o.in.fromPy(p)
	})
}

// MarshalJSON renders o with the json module, so that Object satisfies
// json.Marshaler.
func (o *Object) MarshalJSON() ([]byte, error) {
	return call(o.in, func() ([]byte, error) {
		p, err := o.ptr()
		if err != nil {
			return nil, err
		}
		in := o.in
		if in.jsonDumps == 0 {
			mod := in.importModule("json")
			if mod == 0 {
				return nil, in.pyError()
			}
			defer libpython.XPy_DecRef(in.tls, mod)
			if in.jsonDumps = in.getAttr(mod, "dumps"); in.jsonDumps == 0 {
				return nil, in.pyError()
			}
		}
		s := libpython.XPyObject_CallOneArg(in.tls, in.jsonDumps, p)
		if s == 0 {
			return nil, in.pyError()
		}
		defer libpython.XPy_DecRef(in.tls, s)
		v, err := in.utf8(s)
		if err != nil {
			return nil, err
		}
		return []byte(v), nil
	})
}
