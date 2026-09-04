# cpython-go

A pure Go (no cgo) build of the CPython 3.14 interpreter, produced by
transpiling the CPython C sources with [ccgo](https://gitlab.com/cznic/ccgo)
in the same way [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
and [modernc.org/libquickjs](https://pkg.go.dev/modernc.org/libquickjs) are
built.

Status: experimental. The interpreter boots, runs the pure-Python standard
library (embedded in the binary), supports threads and `subprocess`, and passes
most of CPython's own test suite for the core language and library modules.
Not built: modules that need external C libraries (`zlib`, `_ssl`, `_sqlite3`,
`_ctypes`, `_bz2`, `_lzma`, `_decimal` (the pure-Python `_pydecimal` is used),
`readline`, `_curses`, `_tkinter`); `os.fork` (impossible under the Go
runtime; `subprocess` works via `syscall.ForkExec`).

| GOOS/GOARCH   | Generated | Smoke tests | CPython test suite |
|---------------|-----------|-------------|--------------------|
| darwin/arm64  | yes       | yes         | most modules pass  |
| darwin/amd64  | yes       | yes         | not run yet        |
| linux/amd64   | yes       | yes         | not run yet        |
| linux/arm64   | yes       | yes         | most modules pass  |
| windows/amd64, windows/arm64 | planned (see TODO.md) | | |

Other architectures and FreeBSD are parked; see TODO.md.

## Use

```
go run ./cmd/cpython-go -c 'import json; print(json.dumps({"hello": "world"}))'
go run ./cmd/cpython-go script.py
```

The standard library is extracted once to `$HOME/Library/Caches/cpython-go`
(or the platform cache dir); set `PYTHONHOME` to use another installation.

## Embedding

The root package embeds the interpreter in a Go program:

```go
in, err := cpython.New()
if err != nil {
	log.Fatal(err)
}
defer in.Close()

in.Set("shout", func(s string) string { return strings.ToUpper(s) + "!" })
if err := in.Exec(ctx, "greeting = shout('hello')"); err != nil {
	log.Fatal(err)
}
g, _ := in.Get("greeting")
defer g.Release()
fmt.Println(g.Str()) // HELLO!
```

Caveats: one interpreter per process, everything runs on a single thread (the
Python `threading` module is unsupported), and a panic from the transpiled C
poisons the interpreter. See the package documentation for the value
conversion tables and the full list.

## Regenerate

Needs a CPython 3.14 checkout, clang (Xcode CLT), GNU sed (`brew install
gnu-sed`) and ~30 minutes:

```
git clone --depth 1 --branch 3.14 https://github.com/python/cpython /tmp/cpython-3.14
CPYTHON_SRC=/tmp/cpython-3.14 go run generator.go
go run ./internal/cmd/mkstdlib -o stdlib/python314.zip.gz tmp/cpython/Lib
```

`generator.go` copies the sources to `tmp/cpython`, applies
`internal/patch/*.diff`, runs `configure` natively, then `make libpython3.14.a`
under `ccgo -exec` and links the archives into `libpython/ccgo_<os>_<arch>.go`.
Hand-written libc supplements for darwin live in `libpython/libc_darwin.go`;
`generator.go`'s `shimmedLibc` list routes libc calls that
`modernc.org/libc` does not implement well on darwin to them.

## Layout

- `cmd/cpython-go` — the CLI (same command line as `python3`).
- `libpython` — the transpiled interpreter (`XPy_BytesMain`, `XPy_Initialize`, ...).
- `stdlib` — embedded pure-Python stdlib (`stdlib.Home()`).
- `internal/patch` — small C patches ccgo needs.
- `internal/cmd/typecheck` — go/types checker with exact positions (the Go
  compiler clamps line numbers in million-line files).
- `internal/cmd/mkstdlib` — builds the embedded stdlib zip.
