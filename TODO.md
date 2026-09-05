# TODO

Scope for now: darwin, linux and windows on amd64 and arm64. Everything else
is parked here.

## Platforms

- windows: amd64 runs (CI-validated smoke: threads, subprocess, hashlib,
  pickle, unittest); arm64 is generated and cross-builds, pending hardware
  validation. Winsock shims cover IPv4/IPv6 socket operations and
  `sockaddr_in6`; IPv6 runtime coverage is still pending on Windows hardware.
  Missing: native .pyd loading
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

- Windows (both arches, 55/71 modules fully pass): remaining groups are
  locale/CRT fidelity (setlocale restore, tm_gmtoff), missing optional
  modules (_ctypes, _multiprocessing, _testcapi, _testconsole), Win32 file /
  errno / process semantics in test_os/pathlib/shutil/tempfile/zipfile,
  code-page codecs (CP932...), mmap resize, test_signal exits with
  0xc0000409, and modules exceeding the 90 s budget (math, io, hashlib,
  builtin, asyncio).

## Interpreter gaps

- os.fork: impossible under the Go runtime (raises ENOSYS). multiprocessing
  is not built; subprocess works via syscall.ForkExec (libpython/fork_exec.go).
- Modules needing external C libraries are not built: _ssl, _ctypes, _bz2,
  _lzma, readline, _curses, _tkinter. `_sqlite3` is built against
  modernc.org/libsqlite3; `_decimal` uses bundled libmpdec ANSI64.
- Test-only extension modules (_testcapi, _testinternalcapi, _testlimitedcapi,
  _testmultiphase...) are not built (--disable-test-modules), so tests that
  need them error out.
- Threads: real thread identity is emulated on libc.TLS goroutines;
  pthread_kill remains a stub. Signal masks and waits are emulated per TLS,
  with inherited-mask fallback for newly-created TLS goroutines.

## Known test failures (CPython test suite, darwin/arm64 83/107 and linux/arm64 44/72 modules fully pass)

- darwin: test_socket runs 749 tests with 1 ancillary-data flags failure and
  8 errors (external IDNA lookup plus delayed-signal socket timeouts), with
  268 skipped. IPv6 interface scopes and `/etc/services` lookups work.
  linux/arm64 runs the same 749 tests with 7 failures and 11 errors (delayed
  signals, nonblocking sendmsg timing, and Alpine service lookup), 98 skipped.
  test_logging hangs (~15 min, then SIGALRM watchdog is overridden);
  test_subprocess and test_fcntl end without a summary line (crash/exit to
  diagnose).

- test_io, test_subprocess: hang in blocking pipe reads waiting for EOF
  (fd inherited by a subprocess through ForkExec; not yet diagnosed).
- test_signal: masks, pending signals, sigwait, and all three interval timers
  are implemented. Its only Darwin failure is an isolated child that cannot
  import the test package without a test-inclusive embedded stdlib. Delayed
  delivery from handlers during timed socket operations remains incomplete.
- test_unittest/test_gc: isolated or sanitized children cannot import the test
  package without a test-inclusive embedded stdlib.
- Returned-local reference leaks are fixed on darwin/arm64 (see
  docs/refleak.md); regenerate the other targets from the updated C patch.
  test_weakref's isolated atexit child needs the test-inclusive stdlib.
- test_math: Darwin's CPython `m_tgamma` path, using Go-backed math primitives,
  is 27-787 ulps beyond the test-file tolerances at six cases. Replacing it
  requires a C patch plus regeneration. Linux's same wrapper over transpiled
  musl primitives passes all 89 tests and must not be rerouted.
- Locale collation emulates en_US.UTF-8 primary/base, accent, and case levels
  for Latin-1 and Latin Extended-A. ISO-8859-1 byte ctype folding is supported
  for `re.LOCALE`; `test_locale` and `test_re` pass on Darwin and Linux arm64.
- test_datetime (3): StaticTypes/ExtensionModule tests (test modules absent).
- test_ast: the 500k structural-depth cases are iterative from the ccgo TLS
  stack model's perspective and need an explicit compiler depth guard.
- test_inspect: source for frozen `_collections_abc` is unavailable through
  its aliased `collections.abc` name; one subprocess-origin assertion also
  differs because the child uses the embedded stdlib.
- test_sys: `_stdlib_dir` describes the embedded home while the externally
  supplied test `PYTHONPATH` makes `os.__file__` point at the source checkout;
  the other failure requires omitted `_testcapi` modules.
- test_json/test_argparse: one or a few failures each, not yet triaged.
- Tests that spawn `sys.executable -I/-E` need the test package inside the
  binary: build with `-tags cpython_test` after `make stdlib-tests`
  (package-style test discovery inside the zip still fails for some suites).

## Embedding API

- Interpreter re-entrancy from other goroutines while a host function runs is
  undetected (documented programming error).
- Windows IPv6 behavior still needs runtime validation on Windows hardware;
  both generated targets typecheck and cross-build with `sockaddr_in6` and
  Winsock getaddrinfo/getnameinfo enabled.
