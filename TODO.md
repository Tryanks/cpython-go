# TODO

Scope for now: darwin, linux and windows on amd64 and arm64. Everything else
is parked here.

## Platforms

- windows: amd64 runs (CI-validated smoke: threads, subprocess, hashlib,
  pickle, unittest); arm64 is generated and cross-builds, pending hardware
  validation. Missing: Winsock (modernc TODOs), native .pyd loading
  (GetProcAddress cannot produce ccgo function pointers), full locale
  emulation. See docs/windows.md and internal/builders/windows/RUNTIME.md.
- linux/386, linux/arm, linux/ppc64le, linux/riscv64, linux/s390x — parked.
  `internal/builders/linux/run.sh <arch>` generates them under qemu-user
  (works for all but loong64, which has no container image); expect hours per
  arch and 32-bit fixes in libpython/atomics.go (uint64 atomics on uintptr)
  and cstack.go.
- freebsd/amd64, freebsd/arm64 — parked; needs a qemu VM and a libc
  supplement like libc_darwin.go.
- Cross-platform dedupe of identical generated declarations with
  modernc.org/undup (the repo grows ~37MB per target).

## Interpreter gaps

- os.fork: impossible under the Go runtime (raises ENOSYS). multiprocessing
  is not built; subprocess works via syscall.ForkExec (libpython/fork_exec.go).
- Modules needing external C libraries are not built: zlib (candidate:
  modernc.org/libz linked as another ccgo archive), _ssl, _sqlite3
  (candidate: modernc.org/sqlite's generated code), _ctypes, _bz2, _lzma,
  _decimal (pure-Python _pydecimal is used), readline, _curses, _tkinter.
- Test-only extension modules (_testcapi, _testinternalcapi, _testlimitedcapi,
  _testmultiphase...) are not built (--disable-test-modules), so tests that
  need them error out.
- Threads: real thread identity is emulated on libc.TLS goroutines;
  pthread_kill / signal masks / sigwait are stubs.

## Known test failures (CPython test suite, darwin/arm64 and linux/arm64)

- test_io, test_subprocess: hang in blocking pipe reads waiting for EOF
  (fd inherited by a subprocess through ForkExec; not yet diagnosed).
- test_signal/test_unittest: pending-signal semantics (sigpending, sigwait,
  masks, ITIMER_VIRTUAL/PROF) and delayed delivery of external signals.
- Returned-local reference leaks are fixed on darwin/arm64 (see
  docs/refleak.md); regenerate the other targets from the updated C patch.
  test_weakref's isolated atexit child needs the test-inclusive stdlib.
- test_math: gamma() is ~30 ulps off (Go math.Gamma vs libm tgamma).
- test_locale: collation is ordinal (strcoll/wcscoll).
- test_datetime (3): StaticTypes/ExtensionModule tests (test modules absent).
- test_json/test_re/test_argparse:
  one or a few failures each, not yet triaged.
- Tests that spawn `sys.executable -I/-E` need the test package inside the
  binary: build with `-tags cpython_test` after `make stdlib-tests`
  (package-style test discovery inside the zip still fails for some suites).

## Embedding API

- Interpreter re-entrancy from other goroutines while a host function runs is
  undetected (documented programming error).
- getaddrinfo shim builds IPv4 results only.
