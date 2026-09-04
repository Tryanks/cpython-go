// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"unsafe"

	"modernc.org/libc"

	"github.com/Tryanks/cpython-go/libpython"
)

// toPy converts a Go value to a new strong reference. The caller owns it.
func (in *Interpreter) toPy(v any) (uintptr, error) {
	switch x := v.(type) {
	case nil:
		return libpython.XPy_NewRef(in.tls, none), nil
	case *Object:
		p, err := x.ptr()
		if err != nil {
			return 0, err
		}
		return libpython.XPy_NewRef(in.tls, p), nil
	case bool:
		return libpython.XPyBool_FromLong(in.tls, b2i(x)), nil
	case int:
		return in.fromInt64(int64(x))
	case int8:
		return in.fromInt64(int64(x))
	case int16:
		return in.fromInt64(int64(x))
	case int32:
		return in.fromInt64(int64(x))
	case int64:
		return in.fromInt64(x)
	case uint:
		return in.fromUint64(uint64(x))
	case uint8:
		return in.fromUint64(uint64(x))
	case uint16:
		return in.fromUint64(uint64(x))
	case uint32:
		return in.fromUint64(uint64(x))
	case uint64:
		return in.fromUint64(x)
	case uintptr:
		return in.fromUint64(uint64(x))
	case float32:
		return in.check0(libpython.XPyFloat_FromDouble(in.tls, float64(x)))
	case float64:
		return in.check0(libpython.XPyFloat_FromDouble(in.tls, x))
	case string:
		return in.fromString(x)
	case []byte:
		p := in.cbytes(x)
		defer libc.Xfree(in.tls, p)
		return in.check0(libpython.XPyBytes_FromStringAndSize(in.tls, p, int64(len(x))))
	case *big.Int:
		s := in.cstr(x.String())
		defer libc.Xfree(in.tls, s)
		return in.check0(libpython.XPyLong_FromString(in.tls, s, 0, 10))
	case HostFunc:
		return in.hostFunc("go_func", x)
	case error:
		return 0, fmt.Errorf("%w: Go error values have no Python equivalent", ErrUnsupported)
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		list := libpython.XPyList_New(in.tls, int64(rv.Len()))
		if list == 0 {
			return 0, in.pyError()
		}
		for i := 0; i < rv.Len(); i++ {
			e, err := in.toPy(rv.Index(i).Interface())
			if err != nil {
				libpython.XPy_DecRef(in.tls, list)
				return 0, err
			}
			libpython.XPyList_SetItem(in.tls, list, int64(i), e) // steals e
		}
		return list, nil
	case reflect.Map:
		d := libpython.XPyDict_New(in.tls)
		if d == 0 {
			return 0, in.pyError()
		}
		iter := rv.MapRange()
		for iter.Next() {
			k, err := in.toPy(iter.Key().Interface())
			if err != nil {
				libpython.XPy_DecRef(in.tls, d)
				return 0, err
			}
			e, err := in.toPy(iter.Value().Interface())
			if err != nil {
				libpython.XPy_DecRef(in.tls, k)
				libpython.XPy_DecRef(in.tls, d)
				return 0, err
			}
			rc := libpython.XPyDict_SetItem(in.tls, d, k, e)
			libpython.XPy_DecRef(in.tls, k)
			libpython.XPy_DecRef(in.tls, e)
			if rc != 0 {
				libpython.XPy_DecRef(in.tls, d)
				return 0, in.pyError()
			}
		}
		return d, nil
	case reflect.Func:
		f, err := in.reflectHost("go_func", rv)
		if err != nil {
			return 0, err
		}
		return in.hostFunc("go_func", f)
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return libpython.XPy_NewRef(in.tls, none), nil
		}
	}
	return 0, fmt.Errorf("%w: cannot convert %T to a Python object", ErrUnsupported, v)
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (in *Interpreter) check0(p uintptr) (uintptr, error) {
	if p == 0 {
		return 0, in.pyError()
	}
	return p, nil
}

func (in *Interpreter) fromInt64(v int64) (uintptr, error) {
	return in.check0(libpython.XPyLong_FromLongLong(in.tls, v))
}

func (in *Interpreter) fromUint64(v uint64) (uintptr, error) {
	if v <= math.MaxInt64 {
		return in.fromInt64(int64(v))
	}
	return in.toPy(new(big.Int).SetUint64(v))
}

func (in *Interpreter) fromString(s string) (uintptr, error) {
	p := in.cstr(s)
	defer libc.Xfree(in.tls, p)
	return in.check0(libpython.XPyUnicode_FromStringAndSize(in.tls, p, int64(len(s))))
}

// cbytes copies b into C memory. The result is never NULL, even for an empty
// slice.
func (in *Interpreter) cbytes(b []byte) uintptr {
	p := libc.Xmalloc(in.tls, uint64(len(b))+1)
	if p == 0 {
		panic("cpython: out of memory")
	}
	copy(unsafe.Slice((*byte)(up(p)), len(b)+1), b)
	*(*byte)(up(p + uintptr(len(b)))) = 0
	return p
}

// fromPy converts a borrowed reference to a Go value.
func (in *Interpreter) fromPy(p uintptr) (any, error) {
	switch {
	case p == none:
		return nil, nil
	case in.isa(p, &libpython.XPyBool_Type):
		return libpython.XPyObject_IsTrue(in.tls, p) != 0, nil
	case in.isa(p, &libpython.XPyLong_Type):
		v, err := in.int64(p)
		if err == nil {
			return v, nil
		}
		if !IsException(err, "OverflowError") {
			return nil, err
		}
		return in.bigInt(p)
	case in.isa(p, &libpython.XPyFloat_Type):
		return libpython.XPyFloat_AsDouble(in.tls, p), nil
	case in.isa(p, &libpython.XPyUnicode_Type):
		return in.utf8(p)
	case in.isa(p, &libpython.XPyBytes_Type), in.isa(p, &libpython.XPyByteArray_Type):
		return in.bytes(p)
	case in.isa(p, &libpython.XPyList_Type), in.isa(p, &libpython.XPyTuple_Type),
		in.isa(p, &libpython.XPySet_Type), in.isa(p, &libpython.XPyFrozenSet_Type):
		return in.fromSequence(p)
	case in.isa(p, &libpython.XPyDict_Type):
		return in.fromDict(p)
	}
	return in.obj(libpython.XPy_NewRef(in.tls, p)), nil
}

func (in *Interpreter) fromSequence(p uintptr) ([]any, error) {
	list := libpython.XPySequence_List(in.tls, p)
	if list == 0 {
		return nil, in.pyError()
	}
	defer libpython.XPy_DecRef(in.tls, list)
	n := libpython.XPyObject_Length(in.tls, list)
	r := make([]any, n)
	for i := int64(0); i < n; i++ {
		item := libpython.XPyList_GetItem(in.tls, list, i)
		if item == 0 {
			return nil, in.pyError()
		}
		v, err := in.fromPy(item)
		if err != nil {
			return nil, err
		}
		r[i] = v
	}
	return r, nil
}

// fromDict returns map[string]any when every key is a str and map[any]any
// otherwise.
func (in *Interpreter) fromDict(p uintptr) (any, error) {
	keys := libpython.XPyMapping_Keys(in.tls, p)
	if keys == 0 {
		return nil, in.pyError()
	}
	defer libpython.XPy_DecRef(in.tls, keys)

	n := libpython.XPyObject_Length(in.tls, keys)
	strKeys := true
	ks := make([]any, n)
	vs := make([]any, n)
	for i := int64(0); i < n; i++ {
		k := libpython.XPyList_GetItem(in.tls, keys, i)
		if k == 0 {
			return nil, in.pyError()
		}
		if !in.isa(k, &libpython.XPyUnicode_Type) {
			strKeys = false
		}
		gk, err := in.fromPy(k)
		if err != nil {
			return nil, err
		}
		// A tuple key converts to []any, which cannot be a Go map key; its
		// repr stands in.
		if gk != nil && !reflect.TypeOf(gk).Comparable() {
			gk = in.rawStr(k)
		}
		v := libpython.XPyObject_GetItem(in.tls, p, k)
		if v == 0 {
			return nil, in.pyError()
		}
		gv, err := in.fromPy(v)
		libpython.XPy_DecRef(in.tls, v)
		if err != nil {
			return nil, err
		}
		ks[i], vs[i] = gk, gv
	}
	if strKeys {
		m := make(map[string]any, n)
		for i := range ks {
			m[ks[i].(string)] = vs[i]
		}
		return m, nil
	}
	m := make(map[any]any, n)
	for i := range ks {
		m[ks[i]] = vs[i]
	}
	return m, nil
}
