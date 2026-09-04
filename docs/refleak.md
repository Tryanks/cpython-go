# Returned-local reference leak (darwin/arm64)

The leak comes from ccgo v4.35.0's handling of an inlined, by-value union
parameter in `Include/internal/pycore_stackref.h`, not from Go GC or CPython's
immortality test. CPython's `_PyStackRef` is a union containing `uintptr_t bits`.
In the GIL build, bit 0 (`Py_TAG_REFCNT`) can mark a borrowed stack reference.

`PyStackRef_MakeHeapSafe` converts a borrowed reference to an owned one before
returning or yielding it:

```c
PyObject *obj = BITS_TO_PTR_MASKED(ref);
Py_INCREF(obj);
ref.bits = (uintptr_t)obj;
PyStackRef_CheckValid(ref);
return ref;
```

ccgo spills the union parameter to access its member, but returning the whole
parameter uses the original argument. In `RETURN_VALUE`, the emitted Go writes
the untagged pointer to the spill slot and then returns the original tagged
value:

```go
v293 = retval4
*(*T_PyStackRef)(unsafe.Pointer(bp + 88)) = v293
// ... Py_INCREF(obj10) ...
*(*Tuintptr_t)(unsafe.Pointer(bp + 88)) = uint64(obj10)
v301 = v293
```

The increment has happened, but subsequent stack-reference operations still
treat the result as borrowed. The extra owned reference is lost. Returning a
named local leaks lists, dicts, functions, closures, instances, and generators;
returning a newly created value directly does not need this conversion.
`__aiter__` returning `self` takes the same leaking path.

The source patch constructs and returns a fresh union:

```c
_PyStackRef result = { .bits = (uintptr_t)obj };
PyStackRef_CheckValid(result);
return result;
```

This preserves CPython's C semantics and avoids the compiler defect. The patch
is maintained in `internal/patch/cpython-3.14.diff`; generated files must come
from the generator. Only darwin/arm64 was regenerated for this fix.

A standalone reproduction puts the following inline functions in a header
(ccgo inlines header functions):

```c
typedef union { unsigned long bits; } Ref;
static inline Ref broken(Ref ref) {
    ref.bits &= ~1UL;
    return ref;
}
static inline Ref fixed(Ref ref) {
    Ref result = { .bits = ref.bits & ~1UL };
    return result;
}
```

Called with `{ .bits = 3 }`, native C returns `2` for both; ccgo returns `3`
for `broken` and `2` for `fixed`. The compiler's inline parameter lookup in
`lib/expr.go` selects the spill address for member access but the original
replacement argument for a whole-value expression.

`TestReturnedLocalOwnership` exercises the actual generated evaluator: immediate
weakref callbacks, returned objects of several types, a yielded local, an
async-iterator error path, and cyclic collection. It failed before the fix.

CPython 3.14 borrows some call arguments: `sys.getrefcount(inner)` inside the
outer function can correctly be `1`, whereas 3.12 reports `2`. Both native 3.14
and the fixed interpreter should report `2` after binding the returned function
at module scope. An acyclic function dies immediately; a subsequent
`gc.collect()` can correctly return `0` (also observed on native 3.14). The
regression separately checks a returned self-cycle with a positive collection
count.

The regenerated `RETURN_VALUE` now returns the untagged value:

```go
result3 = T_PyStackRef{Fbits: uint64(obj10)}
v301 = result3
```

Validation on darwin/arm64 (CPython 3.14.7+):

| Suite | Tests | Failures before | Failures after | Skipped |
| --- | ---: | ---: | ---: | ---: |
| test_weakref | 137 | 9 | 1* | 2 |
| test_dataclasses | 280 | 5 subtest failures | 0 | 1 |
| test_copy | 81 | 1 | 0 | 0 |
| test_functools | 325 | 4 | 0 | 1 |
| test_struct | 44 | 1 | 0 | 2 |
| test_coroutines | 100 | 3 | 0 | 3 |
| test_xml_etree | 233 | 1 | 0 | 6 |
| test_itertools | 137 | 1 | 0 | 8 |

There were no unittest errors. `*` The ordinary binary omits `Lib/test`, so
`test_atexit` fails when its `-I` child imports `test.test_weakref`. This failed
before the fix too. Build a test-inclusive binary for that case:

```sh
make stdlib-tests
go build -tags cpython_test -o tmp/refleak/cpython-go-tests ./cmd/cpython-go
PYTHONPATH=$PWD/tmp/darwin_arm64/cpython/Lib perl -e 'alarm 600; exec @ARGV' \
  ./tmp/refleak/cpython-go-tests -m unittest -q test.test_weakref
```

The requested suites were run separately with
`PYTHONPATH=$PWD/tmp/darwin_arm64/cpython/Lib ./tmp/cpython-go -m unittest -q test.test_NAME`,
using a 600-second subprocess timeout for each. The baseline binary was saved
before regeneration. `go test -count=1 .` passes, including the new regression;
`go build -o tmp/typecheck ./internal/cmd/typecheck` followed by
`./tmp/typecheck ./libpython` reports `0 errors`.

The scratch build's Makefile/config.status initially pointed to another
worktree. Those ignored configuration files were relocated to this worktree
before running `GO_GENERATE_INCREMENTAL=1 go run generator.go`. Compilation
and linking completed; a sandbox denial on the Go cache interrupted sharding.
`GO_GENERATE_POSTPROCESS=1 go run generator.go` with cache access completed the
same generator's rewrites and sharding. No generated Go was hand-edited.

The test-inclusive weakref run passed all 137 tests (2 skips, zero failures or
errors), including the isolated atexit child. No distinct async-iterator leak
remains in the minimal reproducer or `test_coroutines`.
