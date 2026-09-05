# cpython-go

A pure Go (no cgo) build of the CPython 3.14 interpreter, produced by
transpiling the CPython C sources with [ccgo](https://gitlab.com/cznic/ccgo)
in the same way [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
and [modernc.org/libquickjs](https://pkg.go.dev/modernc.org/libquickjs) are
built.

Status: experimental. The interpreter boots, runs the pure-Python standard
library (embedded in the binary), supports threads and `subprocess`, and passes
most of CPython's own test suite for the core language and library modules.
Sockets support IPv4 and IPv6, including dual-family `getaddrinfo`, IPv6
address conversion/name lookup, and `AF_INET6` bind/connect. The C `_decimal`
module uses CPython's bundled libmpdec in its portable ANSI64 configuration.
The `zlib` module and zlib-backed `binascii.crc32` use
[`modernc.org/libz`](https://pkg.go.dev/modernc.org/libz), and `_sqlite3` uses
[`modernc.org/libsqlite3`](https://pkg.go.dev/modernc.org/libsqlite3). Not
built: other modules that need external C libraries (`_ssl`, `_ctypes`,
`_bz2`, `_lzma`, `readline`, `_curses`, `_tkinter`); `os.fork` (impossible under the Go runtime;
`subprocess` works via `syscall.ForkExec`).

| GOOS/GOARCH   | Generated | Smoke tests (CI) | CPython test suite |
|---------------|-----------|------------------|--------------------|
| darwin/arm64  | yes       | yes              | 83 of 107 modules fully pass |
| darwin/amd64  | yes       | yes (local)      | 51 of 75 modules fully pass |
| linux/amd64   | yes       | yes              | 48 of 75 modules fully pass |
| linux/arm64   | yes       | yes              | 44 of 72 modules fully pass |
| windows/amd64 | yes       | yes              | 55 of 71 modules fully pass |
| windows/arm64 | yes       | yes              | 55 of 71 modules fully pass |

Other architectures are parked; see TODO.md. Windows details: docs/windows.md;
the Windows CPython test batch runs from `.github/workflows/windows-tests.yml`
(manual trigger).

## Use

```
go run ./cmd/cpython-go -c 'import json; print(json.dumps({"hello": "world"}))'
go run ./cmd/cpython-go script.py
```

The standard library is extracted once to `$HOME/Library/Caches/cpython-go`
(or the platform cache dir); set `PYTHONHOME` to use another installation.

## Performance

The following is a local Darwin/arm64 comparison in seconds (lower is
better). Each in-process workload runs three times with `time.perf_counter`
and reports the minimum; startup is the minimum wall time of three fresh
`-c pass` processes. "Before" is the saved non-PGO cpython-go binary and
"PGO" uses `cmd/cpython-go/default.pgo`. The six-workload elapsed-time sum is
4.1% lower with PGO.

| Workload | Native CPython 3.14 | cpython-go before | cpython-go PGO | PGO change | PGO / native |
| --- | ---: | ---: | ---: | ---: | ---: |
| nbody | 0.055531 | 0.224310 | 0.216804 | -3.3% | 3.90x |
| richards | 0.019012 | 0.076815 | 0.077005 | +0.2% | 4.05x |
| regex | 0.062668 | 0.090183 | 0.088730 | -1.6% | 1.42x |
| json | 0.022035 | 0.050346 | 0.048147 | -4.4% | 2.19x |
| dict / str | 0.111961 | 0.348806 | 0.323817 | -7.2% | 2.89x |
| generator / closure | 0.023237 | 0.099475 | 0.099327 | -0.1% | 4.27x |
| startup | 0.010697 | 0.028045 | 0.029262 | +4.3% | 2.74x |

Exact benchmark commands, run from the repository root:

```sh
GOCACHE=$PWD/tmp/go-cache go build -pgo=off -o tmp/cpython-go-baseline ./cmd/cpython-go
/usr/bin/python3 internal/bench/run.py ./tmp/cpython-go-baseline
/usr/bin/python3 internal/bench/run.py /usr/bin/env \
  PYTHONPATH=tmp/darwin_arm64/cpython/Lib \
  tmp/darwin_arm64/build/python.exe
make pgo
GOCACHE=$PWD/tmp/go-cache go build -o tmp/cpython-go ./cmd/cpython-go
/usr/bin/python3 internal/bench/run.py ./tmp/cpython-go
```

`make pgo` builds an unoptimized-by-PGO training binary, records three CPU
profiles of the suite, merges them, and refreshes `default.pgo`; normal Go
builds then select it automatically. The profile is workload- and
architecture-specific, so refresh it after substantial interpreter changes.
There is no positive Go compiler optimization level to enable through
`-gcflags` (`-N` and `-l` disable optimization and inlining). `GOAMD64` and
`GOARM64` are build environment choices rather than `go.mod` settings. On
this build, `-ldflags='-s -w'` reduced the PGO executable from 37,783,602 to
27,135,634 bytes (28.2%); it strips symbol/debug metadata and is a size, not
an execution-speed, option.

Pymalloc was also tested after a full Darwin/arm64 regeneration. All requested
core suites passed, but alternating benchmarks showed roughly 1-3% regressions
in Richards, regex, and JSON and about 4% in the dict/string workload, so the
transpiled build continues to use `--without-pymalloc`. Darwin's libc shim has
working `mmap`/`munmap`; Windows pymalloc regeneration would instead exercise
its `VirtualAlloc`/`VirtualFree` paths.

These are small microbenchmarks on one machine, not pyperformance-calibrated
results. Minimum-of-three reduces incidental scheduler noise but does not
provide confidence intervals, and startup includes stdlib/cache and OS effects.

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
go run ./internal/cmd/mkstdlib -o stdlib/python314.zip tmp/cpython/Lib
```

`generator.go` copies the sources to `tmp/cpython`, applies
`internal/patch/*.diff`, runs `configure` natively, then `make libpython3.14.a`
under `ccgo -exec` and links the archives into `libpython/ccgo_<os>_<arch>.go`.
The generated interpreter links CPython's bundled libmpdec archive, zlib calls
to `modernc.org/libz`, and SQLite calls to `modernc.org/libsqlite3`.
Hand-written libc supplements live in
`libpython/libc_*.go`; `generator.go`'s `shimmedLibc` lists route libc calls
that `modernc.org/libc` does not implement well to them.

## Layout

- `cmd/cpython-go` — the CLI (same command line as `python3`).
- `libpython` — the transpiled interpreter (`XPy_BytesMain`, `XPy_Initialize`, ...).
- `stdlib` — embedded pure-Python stdlib (`stdlib.Home()`).
- `internal/patch` — small C patches ccgo needs.
- `internal/cmd/typecheck` — go/types checker with exact positions (the Go
  compiler clamps line numbers in million-line files).
- `internal/cmd/mkstdlib` — builds the embedded stdlib zip.
