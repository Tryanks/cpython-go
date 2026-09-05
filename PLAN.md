# cpython-go — pure Go CPython via ccgo

Goal: a pure Go (no cgo) Python interpreter produced by transpiling CPython
with `modernc.org/ccgo/v4`, in the style of `modernc.org/libquickjs` +
`modernc.org/quickjs` and `modernc.org/sqlite`.

## Decisions

- CPython 3.14 branch (source at `$CPYTHON_SRC`, copied to `tmp/cpython` and
  patched). mimalloc, the JIT, free-threading and remote debugging are off.
- Primary target: darwin/arm64 (dev machine). On darwin ccgo parses against
  the macOS SDK headers and links against the thin hand-written
  `modernc.org/libc` (no musl there), so missing libc symbols are expected and
  are supplied by our own Go package (see `libpython/libcx*.go` / `-L -l`
  mechanism of ccgo: `-L<import-prefix> -l<name>` resolves Go packages
  exporting `X`-prefixed funcs).
- Build: native `configure` (static, no dlopen, no pkg-config, external-lib
  modules marked `n/a`), then `ccgo -exec make libpython3.14.a`. The ccgo cc
  shim runs the real compiler first, so `_freeze_module`/`_bootstrap_python`
  work; each `.o` gets a sibling `.o.go`; `ar` produces `.ago`.
- Final link: `libpython3.14.a` + `Modules/_hacl/*.a` + `Modules/expat/libexpat.a`
  → one file, then rewrites + sharding (internal/cmd/splitgo) into
  `libpython/ccgo_<goos>_<goarch>_NN.go` + `_data.bin`.
- `--without-computed-gotos`, `--without-pymalloc`, `-U__SIZEOF_INT128__`.
- Stdlib `.py` files: milestone 1 uses `PYTHONHOME` pointing at a native
  `make install` prefix (`/tmp/cpy-prefix`) or `PYTHONPATH` to `Lib/`.
  Embedding comes later.

## Layout

- `generator.go` — `go run generator.go` regenerates everything.
- `libpython/` — generated `ccgo_*.go` + hand-written shims for symbols the
  darwin libc lacks.
- `cmd/cpython-go/` — CLI: `cpython-go [args]` == `python3 [args]`.
- `tmp/` — scratch (gitignored): `tmp/build` configure+make dir.

## Milestone 1 acceptance (executable)

```
CPYTHON_SRC=/tmp/cpython-3.14 go run generator.go                    # exit 0
go build ./...                                                          # exit 0
go run ./cmd/cpython-go -c 'print(sum(range(10)))'                      # prints 45
go run ./cmd/cpython-go -c 'import json, re, os; print(json.dumps({"a": [1, 2]}))'
                                                                        # prints {"a": [1, 2]}
```

## Progress log

- M1 done (2026-09-04): boots, embedded stdlib, 11/12 smoke snippets, core
  Lib/test modules (grammar, class, generators, sort, bisect, textwrap) pass.
- Generated file: 61MB/2.2M lines lean (90MB with GO_GENERATE_DEV positions).
  36MB is static data (25MB plain integer tables). Plan: `internal/cmd/splitgo`
  shards the file and moves integer tables into an embedded blob.
- Known ccgo issues worked around (see generator.go comments and
  internal/patch): parenthesized string-literal array init, designated union
  member init, dropped `static inline` bodies, `__ccgo_fp(X)(...)` calls,
  `int8(ENUM)` overflow, clang builtin `<limits.h>` shadowing the SDK copy.

## Status (2026-09-05)

- Switching to CPython 3.14 on branch py314 (worktree ../cpython-go-314):
  all sources transpile; remaining work is shims (atomics, virtual C stack,
  fork_exec in Go) — see internal/patch/cpython-3.14.diff for the C patches.
- Embedding API (package cpython) done: Interpreter/Object/host functions,
  crash isolation (panics → CrashError, exit → ExitError).
- Generated code is sharded by internal/cmd/splitgo (12 files + data blob).
- C `_Thread_local` variables are lowered to per-libc.TLS slots by the
  generator (libpython/tls.go), so Python threads work. fork() is not
  possible under the Go runtime: subprocess uses syscall.ForkExec
  (libpython/fork_exec.go); os.fork raises. The zlib extension and
  zlib-backed binascii CRC32 link to modernc.org/libz; `_sqlite3` links to
  modernc.org/libsqlite3. Other modules needing external C libraries are not
  built.
- Platform scope: darwin, linux, windows × amd64, arm64 — all six generated.
  Linux via OrbStack containers (internal/builders/linux/run.sh); Windows via
  an llvm-mingw container (internal/builders/windows/run.sh --ccgo <arch>)
  with the MSYS2 mingw patch set; windows/amd64 runtime validated on GitHub
  Actions. Everything else is parked in TODO.md.
- A ccgo miscompilation (by-value union parameter returned after modifying a
  member) leaked one reference per returned local; worked around in
  PyStackRef_MakeHeapSafe (docs/refleak.md).

## Milestone 2

- `go test ./...` runs a subset of `Lib/test` (e.g. test_grammar, test_int,
  test_dict, test_json, test_re) through the Go binary.
- Embedded stdlib (deflated zip on sys.path), no PYTHONHOME needed.
- Public Go API (`cpython.New()`, `Run(src)`), modelled after
  `modernc.org/quickjs`.

## Milestone 3

- linux/amd64 generation (needs a Linux host: musl-based libc there is far
  more complete).
- Threads (`_thread`) via libc pthread emulation; signals; sockets.
