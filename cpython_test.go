// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tryanks/cpython-go"
)

// newInterp returns an interpreter closed at the end of the test. Only one
// can be open at a time, so tests must not run in parallel.
func newInterp(t *testing.T, opts ...cpython.Option) *cpython.Interpreter {
	t.Helper()
	in, err := cpython.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := in.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return in
}

func TestNewCloseTwice(t *testing.T) {
	for i := 0; i < 2; i++ {
		in, err := cpython.New()
		if err != nil {
			t.Fatalf("New #%d: %v", i, err)
		}
		if _, err := cpython.New(); !errors.Is(err, cpython.ErrAlreadyOpen) {
			t.Errorf("second New: got %v, want ErrAlreadyOpen", err)
		}
		if err := in.Exec(context.Background(), "x = 1"); err != nil {
			t.Fatalf("Exec #%d: %v", i, err)
		}
		if err := in.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
		if err := in.Exec(context.Background(), "x = 1"); !errors.Is(err, cpython.ErrClosed) {
			t.Errorf("Exec after Close: got %v, want ErrClosed", err)
		}
	}
}

func TestVersion(t *testing.T) {
	if v := cpython.Version(); !strings.HasPrefix(v, "3.") {
		t.Errorf("Version = %q", v)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	in := newInterp(t)
	big1 := new(big.Int).Lsh(big.NewInt(1), 100)
	for _, tc := range []struct {
		name string
		set  any
		want any
	}{
		{"nil", nil, nil},
		{"true", true, true},
		{"false", false, false},
		{"int", 42, int64(42)},
		{"int8", int8(-3), int64(-3)},
		{"int64", int64(1 << 40), int64(1 << 40)},
		{"uint", uint(7), int64(7)},
		{"uint64max", uint64(1<<64 - 1), new(big.Int).SetUint64(1<<64 - 1)},
		{"float32", float32(1.5), 1.5},
		{"float64", 2.25, 2.25},
		{"string", "héllo", "héllo"},
		{"bytes", []byte{1, 2, 3}, []byte{1, 2, 3}},
		{"bigint", big1, big1},
		{"slice", []any{1, "a", nil}, []any{int64(1), "a", nil}},
		{"intslice", []int{1, 2}, []any{int64(1), int64(2)}},
		{"map", map[string]any{"a": 1}, map[string]any{"a": int64(1)}},
		{"intmap", map[int]string{1: "x"}, map[any]any{int64(1): "x"}},
		{"nested", map[string]any{"a": []any{1, map[string]any{"b": true}}},
			map[string]any{"a": []any{int64(1), map[string]any{"b": true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := in.Set("v", tc.set); err != nil {
				t.Fatalf("Set: %v", err)
			}
			o, err := in.Get("v")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			defer o.Release()
			got, err := o.Value()
			if err != nil {
				t.Fatalf("Value: %v", err)
			}
			if b, ok := tc.want.(*big.Int); ok {
				if g, ok := got.(*big.Int); !ok || g.Cmp(b) != 0 {
					t.Fatalf("got %v (%T), want %v", got, got, b)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSetUnsupported(t *testing.T) {
	in := newInterp(t)
	if err := in.Set("v", make(chan int)); !errors.Is(err, cpython.ErrUnsupported) {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}

func TestGetMissing(t *testing.T) {
	in := newInterp(t)
	_, err := in.Get("nope")
	if !cpython.IsException(err, "NameError") {
		t.Errorf("got %v, want a NameError", err)
	}
}

func TestEval(t *testing.T) {
	in := newInterp(t)
	for _, tc := range []struct {
		expr string
		want any
	}{
		{"1 + 1", int64(2)},
		{"2 ** 100", new(big.Int).Lsh(big.NewInt(1), 100)},
		{"1 / 4", 0.25},
		{`"a" * 3`, "aaa"},
		{`b"xy"`, []byte("xy")},
		{"[1, 'a']", []any{int64(1), "a"}},
		{"(1, 2)", []any{int64(1), int64(2)}},
		{"{'k': [1]}", map[string]any{"k": []any{int64(1)}}},
		{"{1: 'a'}", map[any]any{int64(1): "a"}},
		{"{1, 2}", []any{int64(1), int64(2)}},
		{"None", nil},
		{"True", true},
	} {
		o, err := in.Eval(context.Background(), tc.expr)
		if err != nil {
			t.Fatalf("Eval(%q): %v", tc.expr, err)
		}
		got, err := o.Value()
		o.Release()
		if err != nil {
			t.Fatalf("Value(%q): %v", tc.expr, err)
		}
		if b, ok := tc.want.(*big.Int); ok {
			if g, ok := got.(*big.Int); !ok || g.Cmp(b) != 0 {
				t.Errorf("%s: got %v (%T), want %v", tc.expr, got, got, b)
			}
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.expr, got, tc.want)
		}
	}
}

func TestAccessors(t *testing.T) {
	in := newInterp(t)
	o, err := in.Eval(context.Background(), "{'a': 1, 'b': 2}")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()

	if n, err := o.Len(); err != nil || n != 2 {
		t.Errorf("Len = %v, %v; want 2", n, err)
	}
	if o.Type() != "dict" {
		t.Errorf("Type = %q, want dict", o.Type())
	}
	if o.IsNone() {
		t.Error("IsNone = true")
	}
	item, err := o.Item("a")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := item.Int(); err != nil || v != 1 {
		t.Errorf(`o["a"] = %v, %v; want 1`, v, err)
	}
	item.Release()
	if err := o.SetItem("c", 3); err != nil {
		t.Fatal(err)
	}
	if n, _ := o.Len(); n != 3 {
		t.Errorf("Len after SetItem = %d, want 3", n)
	}
	keys, err := o.Slice()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range keys {
		s, err := k.String()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
		k.Release()
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Slice = %v, want %v", got, want)
	}
	if o.Repr() != "{'a': 1, 'b': 2, 'c': 3}" {
		t.Errorf("Repr = %q", o.Repr())
	}

	// Attr/SetAttr need an object with a __dict__.
	if err := in.Exec(context.Background(), "class C: pass\nc = C()"); err != nil {
		t.Fatal(err)
	}
	c, err := in.Get("c")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	if err := c.SetAttr("x", "hi"); err != nil {
		t.Fatal(err)
	}
	x, err := c.Attr("x")
	if err != nil {
		t.Fatal(err)
	}
	defer x.Release()
	if s, err := x.String(); err != nil || s != "hi" {
		t.Errorf("c.x = %q, %v; want hi", s, err)
	}
	if _, err := c.Attr("nope"); !cpython.IsException(err, "AttributeError") {
		t.Errorf("Attr(nope) = %v, want AttributeError", err)
	}
	if c.Type() != "C" {
		t.Errorf("Type = %q, want C", c.Type())
	}
}

func TestObjectCall(t *testing.T) {
	in := newInterp(t)
	if err := in.Exec(context.Background(), "def f(a, b=0, *, c=0): return a + b + c"); err != nil {
		t.Fatal(err)
	}
	f, err := in.Get("f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	r, err := f.Call(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Int(); v != 3 {
		t.Errorf("f(1, 2) = %d, want 3", v)
	}
	r.Release()

	r, err = f.CallKw([]any{1}, map[string]any{"c": 10})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Int(); v != 11 {
		t.Errorf("f(1, c=10) = %d, want 11", v)
	}
	r.Release()

	if _, err := f.Call(); !cpython.IsException(err, "TypeError") {
		t.Errorf("f() = %v, want TypeError", err)
	}
}

func TestException(t *testing.T) {
	in := newInterp(t)
	err := in.Exec(context.Background(), "raise ValueError('boom')")
	var pe *cpython.Error
	if !errors.As(err, &pe) {
		t.Fatalf("got %T %v, want *cpython.Error", err, err)
	}
	if pe.Type != "ValueError" || pe.Message != "boom" {
		t.Errorf("Type/Message = %q/%q", pe.Type, pe.Message)
	}
	if pe.Error() != "ValueError: boom" {
		t.Errorf("Error() = %q", pe.Error())
	}
	if !strings.Contains(pe.Traceback, "ValueError: boom") {
		t.Errorf("Traceback = %q", pe.Traceback)
	}
	if pe.Exception == nil || pe.Exception.Type() != "ValueError" {
		t.Errorf("Exception = %v", pe.Exception)
	}
	if !cpython.IsException(err, "ValueError") {
		t.Error("IsException(ValueError) = false")
	}
}

func TestExecFile(t *testing.T) {
	in := newInterp(t)
	path := filepath.Join(t.TempDir(), "script.py")
	src := "def boom():\n    raise KeyError('nope')\n\n\nresult = 21 * 2\nboom()\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	err := in.ExecFile(context.Background(), path)
	var pe *cpython.Error
	if !errors.As(err, &pe) {
		t.Fatalf("got %T %v, want *cpython.Error", err, err)
	}
	if pe.Type != "KeyError" {
		t.Errorf("Type = %q", pe.Type)
	}
	// The traceback quotes the file it came from, including its source.
	if !strings.Contains(pe.Traceback, path) {
		t.Errorf("Traceback lacks %q:\n%s", path, pe.Traceback)
	}
	if !strings.Contains(pe.Traceback, "raise KeyError('nope')") {
		t.Errorf("Traceback lacks the source line:\n%s", pe.Traceback)
	}
	// Statements before the failure ran.
	v, err := in.Get("result")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	if n, _ := v.Int(); n != 42 {
		t.Errorf("result = %d, want 42", n)
	}
}

func TestSysExit(t *testing.T) {
	in := newInterp(t)
	for _, tc := range []struct {
		code string
		want int
	}{
		{"import sys; sys.exit(3)", 3},
		{"import sys; sys.exit()", 0},
		{"import sys; sys.exit('bye')", 1},
	} {
		err := in.Exec(context.Background(), tc.code)
		var xe *cpython.ExitError
		if !errors.As(err, &xe) {
			t.Fatalf("%s: got %T %v, want *cpython.ExitError", tc.code, err, err)
		}
		if xe.Code != tc.want {
			t.Errorf("%s: Code = %d, want %d", tc.code, xe.Code, tc.want)
		}
	}
	// The interpreter survives sys.exit.
	if err := in.Exec(context.Background(), "y = 1"); err != nil {
		t.Errorf("Exec after sys.exit: %v", err)
	}
}

func TestContextCancel(t *testing.T) {
	in := newInterp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := in.Exec(ctx, "while True:\n    pass\n")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Exec took %v", d)
	}
	if !cpython.IsException(err, "KeyboardInterrupt") {
		t.Fatalf("got %v (%T), want a KeyboardInterrupt", err, err)
	}
	// The watcher goroutine is gone and the interpreter still works.
	if err := in.Exec(context.Background(), "z = 1"); err != nil {
		t.Errorf("Exec after interrupt: %v", err)
	}
}

func TestFunc(t *testing.T) {
	in := newInterp(t)
	repeat := func(n int, s string) (string, error) {
		if n < 0 {
			return "", fmt.Errorf("negative count %d", n)
		}
		return strings.Repeat(s, n), nil
	}
	if err := in.Set("repeat", repeat); err != nil {
		t.Fatal(err)
	}
	o, err := in.Eval(context.Background(), `repeat(3, "ab")`)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := o.String(); s != "ababab" {
		t.Errorf(`repeat(3, "ab") = %q`, s)
	}
	o.Release()

	err = in.Exec(context.Background(), `repeat(-1, "x")`)
	if !cpython.IsException(err, "RuntimeError") {
		t.Fatalf("got %v, want RuntimeError", err)
	}
	var pe *cpython.Error
	errors.As(err, &pe)
	if !strings.Contains(pe.Message, "negative count -1") {
		t.Errorf("Message = %q", pe.Message)
	}

	// Wrong argument count and wrong type are reported, not crashed on.
	if err := in.Exec(context.Background(), `repeat(1)`); !cpython.IsException(err, "RuntimeError") {
		t.Errorf("repeat(1) = %v, want RuntimeError", err)
	}
	if err := in.Exec(context.Background(), `repeat("x", "y")`); !cpython.IsException(err, "RuntimeError") {
		t.Errorf(`repeat("x", "y") = %v, want RuntimeError`, err)
	}

	// Variadic, no result, and *Object parameters.
	if err := in.Set("total", func(vs ...int) int {
		n := 0
		for _, v := range vs {
			n += v
		}
		return n
	}); err != nil {
		t.Fatal(err)
	}
	o, err = in.Eval(context.Background(), "total(1, 2, 3)")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := o.Int(); v != 6 {
		t.Errorf("total(1, 2, 3) = %d, want 6", v)
	}
	o.Release()

	var seen string
	if err := in.Set("show", func(o *cpython.Object) { seen = o.Repr() }); err != nil {
		t.Fatal(err)
	}
	if err := in.Exec(context.Background(), "show({'a': 1})"); err != nil {
		t.Fatal(err)
	}
	if seen != "{'a': 1}" {
		t.Errorf("seen = %q", seen)
	}
}

func TestNewFuncKwargs(t *testing.T) {
	in := newInterp(t)
	f, err := in.NewFunc("describe", func(args []*cpython.Object, kwargs map[string]*cpython.Object) (any, error) {
		var b strings.Builder
		for _, a := range args {
			fmt.Fprintf(&b, "%s;", a.Str())
		}
		for _, k := range []string{"x", "y"} {
			if v, ok := kwargs[k]; ok {
				fmt.Fprintf(&b, "%s=%s;", k, v.Str())
			}
		}
		return b.String(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Set("describe", f); err != nil {
		t.Fatal(err)
	}
	o, err := in.Eval(context.Background(), `describe(1, "a", y=2, x=3)`)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	if s, _ := o.String(); s != "1;a;x=3;y=2;" {
		t.Errorf("got %q", s)
	}
}

// A host function may call back into the interpreter it is running in.
func TestHostFuncReentrant(t *testing.T) {
	in := newInterp(t)
	if err := in.Set("double", func(o *cpython.Object) (any, error) {
		v, err := o.Item(0)
		if err != nil {
			return nil, err
		}
		defer v.Release()
		n, err := v.Int()
		if err != nil {
			return nil, err
		}
		return n * 2, nil
	}); err != nil {
		t.Fatal(err)
	}
	o, err := in.Eval(context.Background(), "double([21])")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	if v, _ := o.Int(); v != 42 {
		t.Errorf("double([21]) = %d, want 42", v)
	}
}

func TestMarshalJSON(t *testing.T) {
	in := newInterp(t)
	o, err := in.Eval(context.Background(), `{"a": [1, 2], "b": None}`)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	// json.Marshal compacts what MarshalJSON returned.
	if got := string(b); got != `{"a":[1,2],"b":null}` {
		t.Errorf("got %s", got)
	}
	bad, err := in.Eval(context.Background(), "object()")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Release()
	if _, err := json.Marshal(bad); err == nil {
		t.Error("marshaling object() succeeded")
	}
}

func TestWithStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	in := newInterp(t, cpython.WithStdout(&out), cpython.WithStderr(&errOut))
	if err := in.Exec(context.Background(), "import sys\nprint('hello', 42)\nprint('bad', file=sys.stderr)\n"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello 42\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.String() != "bad\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestWithPathAndArgv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mymod.py"), []byte("VALUE = 'from mymod'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := newInterp(t, cpython.WithPath(dir), cpython.WithArgv([]string{"prog", "-x"}))

	m, err := in.Import("mymod")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release()
	v, err := m.Attr("VALUE")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	if s, _ := v.String(); s != "from mymod" {
		t.Errorf("mymod.VALUE = %q", s)
	}

	o, err := in.Eval(context.Background(), "__import__('sys').argv")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	got, _ := o.Value()
	if want := []any{"prog", "-x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sys.argv = %#v, want %#v", got, want)
	}
}

func TestImport(t *testing.T) {
	in := newInterp(t)
	m, err := in.Import("json")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release()
	dumps, err := m.Attr("dumps")
	if err != nil {
		t.Fatal(err)
	}
	defer dumps.Release()
	r, err := dumps.Call([]any{1, "x"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if s, _ := r.String(); s != `[1, "x"]` {
		t.Errorf("json.dumps = %q", s)
	}
	if _, err := in.Import("no_such_module_here"); !cpython.IsException(err, "ModuleNotFoundError") {
		t.Errorf("got %v, want ModuleNotFoundError", err)
	}
}

func TestScalarAccessors(t *testing.T) {
	in := newInterp(t)
	o, err := in.Eval(context.Background(), "2 ** 100")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	if _, err := o.Int(); !cpython.IsException(err, "OverflowError") {
		t.Errorf("Int on a huge int = %v, want OverflowError", err)
	}
	z, err := o.BigInt()
	if err != nil {
		t.Fatal(err)
	}
	if z.Cmp(new(big.Int).Lsh(big.NewInt(1), 100)) != 0 {
		t.Errorf("BigInt = %v", z)
	}
	if b, err := o.Bool(); err != nil || !b {
		t.Errorf("Bool = %v, %v", b, err)
	}
	if _, err := o.String(); !cpython.IsException(err, "TypeError") {
		t.Errorf("String on an int = %v, want TypeError", err)
	}
	if o.Str() != "1267650600228229401496703205376" {
		t.Errorf("Str = %q", o.Str())
	}

	f, err := in.Eval(context.Background(), "1.5")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()
	if v, err := f.Float(); err != nil || v != 1.5 {
		t.Errorf("Float = %v, %v", v, err)
	}

	ba, err := in.Eval(context.Background(), "bytearray(b'hi')")
	if err != nil {
		t.Fatal(err)
	}
	defer ba.Release()
	if b, err := ba.Bytes(); err != nil || string(b) != "hi" {
		t.Errorf("Bytes = %q, %v", b, err)
	}

	// Release is idempotent and reported afterwards.
	n := in.None()
	if !n.IsNone() {
		t.Error("None is not None")
	}
	n.Release()
	n.Release()
	if _, err := n.Int(); err == nil {
		t.Error("Int on a released object succeeded")
	}
}

func Example_basic() {
	in, err := cpython.New()
	if err != nil {
		panic(err)
	}
	defer in.Close()

	if err := in.Exec(context.Background(), "import math\nr = math.hypot(3, 4)\n"); err != nil {
		panic(err)
	}
	r, err := in.Get("r")
	if err != nil {
		panic(err)
	}
	defer r.Release()
	v, _ := r.Float()
	fmt.Println(v)
	// Output: 5
}

func Example_hostFunction() {
	in, err := cpython.New(cpython.WithStdout(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer in.Close()

	if err := in.Set("shout", func(s string) string { return strings.ToUpper(s) + "!" }); err != nil {
		panic(err)
	}
	if err := in.Exec(context.Background(), "print(shout('hello from python'))"); err != nil {
		panic(err)
	}
	// Output: HELLO FROM PYTHON!
}
