// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package cpython_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cpython-go-test")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "cpython-go")
	out, err := exec.Command("go", "build", "-o", bin, "./cmd/cpython-go").CombinedOutput()
	if err != nil {
		panic(string(out))
	}
	rc := m.Run()
	os.RemoveAll(dir)
	os.Exit(rc)
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSmoke(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{`print(sum(range(10)))`, "45"},
		{`import json, re, os; print(json.dumps({"a": [1, 2]}))`, `{"a": [1, 2]}`},
		{`import sys; print(sys.platform, sys.version_info[:2])`, runtime.GOOS + " (3, 14)"},
		{`print(2**100)`, "1267650600228229401496703205376"},
		{`print(sorted({"b": 1, "a": 2}.items()))`, "[('a', 2), ('b', 1)]"},
		{`import re; print(re.sub(r"(\w+)@(\w+)", r"\2 at \1", "me@host"))`, "host at me"},
		{`print(f"{3.14159:.2f} {'x'!r} {1_000_000:,}")`, "3.14 'x' 1,000,000"},
		{`class A:
    def __init__(self): self.x = 1
print(A().x, isinstance(A(), object))`, "1 True"},
		{`import decimal, fractions; print(fractions.Fraction(1, 3) + fractions.Fraction(1, 6))`, "1/2"},
		{`import datetime; print(datetime.date(2026, 9, 4).isoformat())`, "2026-09-04"},
		{`import hashlib; print(hashlib.sha256(b"abc").hexdigest()[:16])`, "ba7816bf8f01cfea"},
		{`import hashlib; print(hashlib.blake2b(b"abc").hexdigest()[:16], hashlib.blake2s(b"abc").hexdigest()[:8])`, "ba80a53f981c4d0d 508c5e8c"},
		{`import zlib, gzip; print(zlib.crc32(b"abc"), gzip.decompress(gzip.compress(b"hi")))`, "891568578 b'hi'"},
		{`import unittest; print(unittest.TestCase.__name__)`, "TestCase"},
	} {
		if got := run(t, "-c", tc.code); got != tc.want {
			t.Errorf("%s\n got: %q\nwant: %q", tc.code, got, tc.want)
		}
	}
}

// Returning a borrowed local must transfer the owned reference created when
// making the stack reference heap-safe. Exercise the generated evaluator.
func TestReturnedLocalOwnership(t *testing.T) {
	got := run(t, "-c", `
import gc, sys, weakref

def outer():
    def inner(): pass
    return inner

f = outer()
assert sys.getrefcount(f) == 2
called = []
r = weakref.ref(f, lambda w: called.append(True))
del f
assert r() is None
assert called == [True]

for expression in ('[]', '{}', 'lambda: None',
                   '(lambda x: lambda: x)(42)',
                   'type("A", (), {})()', '(x for x in ())'):
    scope = {}
    exec('def factory():\n obj = ' + expression + '\n return obj', scope)
    obj = scope['factory']()
    assert sys.getrefcount(obj) == 2, expression

def yielding():
    obj = []
    yield obj

g = yielding()
obj = next(g)
assert sys.getrefcount(obj) == 3
g.close()
assert sys.getrefcount(obj) == 2

class AsyncIterator:
    def __aiter__(self):
        return self

obj = AsyncIterator()
async def consume():
    async for item in obj:
        pass

c = consume()
try:
    c.send(None)
except TypeError:
    pass
else:
    raise AssertionError('missing __anext__ must raise TypeError')
assert sys.getrefcount(obj) == 2

def cycle():
    class A: pass
    obj = A()
    obj.self = obj
    return obj

obj = cycle()
r = weakref.ref(obj)
del obj
assert gc.collect() > 0
assert r() is None
print('ok')
`)
	if got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
}
