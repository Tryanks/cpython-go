# Windows targets (windows/amd64 and windows/arm64)

CPython 3.14.7 is transpiled with ccgo and built as a pure-Go executable for
both Windows architectures. The generated sources live in
`libpython/ccgo_windows_<arch>_*.go`; `libpython/libc_windows.go` supplies the
UCRT, Win32, and Winsock surface that modernc.org/libc does not implement or
implements with an incompatible ABI.

## Current validation

Both Windows jobs in `.github/workflows/ci.yml` are required. They build and
run the interpreter on `windows-latest` and `windows-11-arm`, then cover:

- startup, imports, threading, hashing, pickle, regex, datetime, and unittest;
- `os.pipe`, process creation/waiting, subprocess output, and pipe EOF;
- `socket.getaddrinfo`, loopback TCP echo, `select.select`, and
  `socket.socketpair()`;
- `asyncio.sleep(0)` and a Proactor TCP client/server using
  `asyncio.start_server`; and
- an `http.client` GET against a local `http.server` thread.

Run 33958794432 passed this suite on Windows amd64 and arm64 together with the
Linux amd64, Linux arm64, and macOS jobs.

The broader `.github/workflows/windows-tests.yml` runs on pushes to
`windows-*` branches and by manual dispatch. It checks out CPython v3.14.7,
builds `stdlib/python314_tests.zip`, builds the interpreter with the
`cpython_test` tag, and runs the selected CPython `test.*` modules in separate
processes. Each module has a 300-second limit and closed stdin, so slow suites
can complete and `breakpoint()` cannot attach an interactive pdb session to
the runner. Manual dispatch accepts comma-separated qualified unittest
module/class/method selectors. The workflow writes a Markdown table to the job
summary and uploads the table plus complete logs as an artifact for each
architecture.

The September 2026 polish batch is recorded by Actions run 33974641629 at
commit `a98943e`: 55 of 71 modules were OK on each architecture, up from 38 of
71 in run 33962400678, with no regression among the modules that were already
passing. Exact per-module before/after results and residual categories are in
`internal/builders/windows/NOTES.md` and
`internal/builders/windows/TODO.md`.

The Windows runtime keeps Win32 LastError independent from CRT `errno` and
maps file failures into UCRT-compatible `errno`/`__doserrno`. Direct
`CreateFileW` descriptor handling preserves UTF-16 surrogate paths, Windows
sharing/delete-on-close, exclusive creation, text/binary translation, and
invalid-descriptor behavior. Locale and time calls use UCRT state and names;
unsupported signal numbers are rejected before UCRT's fail-fast invalid-
parameter handler.

Wide and narrow environment queries share the live process environment, so
changes made through `os.environ` are visible to both `_wgetenv` and
`Py_GETENV`/`getenv` instead of modernc's startup cache.

Generated C frames allocate from modernc's TLS stack, so Windows publishes a
matching virtual 8 MiB stack range to CPython's recursion guard. A deep JSON
encoding smoke verifies that recursive extension-module calls raise
`RecursionError` instead of exhausting the Go goroutine stack.

## Winsock and overlapped I/O

Generated calls to modernc's TODO socket functions are routed to Windows
implementations. The shims preserve Win64 `SOCKET`, `SOCKET_ERROR`,
`INVALID_SOCKET`, native sockaddr memory, Winsock `fd_set` (a count plus an
array of sockets), and immediate WSA error capture in ccgo TLS.

CPython's Proactor normally obtains `AcceptEx`, `ConnectEx`, `DisconnectEx`,
and `TransmitFile` addresses with `SIO_GET_EXTENSION_FUNCTION_POINTER`. A
native address cannot be invoked as a ccgo Go function value. A narrowly
guarded `MS_WINDOWS && CCGO && __CCGO__` patch therefore calls Go bridge
symbols directly; native object compilation keeps CPython's original path.
The bridges use `golang.org/x/sys/windows` or a native syscall trampoline and
retain normal overlapped/`ERROR_IO_PENDING` behavior. Completion is finalized
through the routed `GetOverlappedResult` implementation.

`RegisterWaitForSingleObject`, used by Proactor process and handle waits, uses
a fixed native Go callback trampoline. It carries an opaque token instead of a
Go pointer into kernel32, creates callback-local libc TLS, and then dispatches
the translated ccgo callback, which posts the completion to IOCP.

## Regeneration

The pinned builder uses the MSYS2 CPython 3.14.7 MinGW patch set and
llvm-mingw. From the repository root, with no other project builder container
running:

```sh
internal/builders/windows/run.sh --ccgo amd64 /tmp/cpython-3.14.7
WINDOWS_BUILDER_SKIP_BUILD=1 internal/builders/windows/run.sh --ccgo arm64 /tmp/cpython-3.14.7
```

`generator.go` applies the project patch before the pinned MSYS2 patch,
transpiles each archive member, shards the result, and rewrites the sorted
per-OS `shimmedLibc` calls to their `_ccgo_*` implementations. See
`internal/builders/windows/NOTES.md` for builder details and
`internal/builders/windows/RUNTIME.md` for the function-by-function runtime
contract.

## Known limits

Native APIs cannot consume an arbitrary transpiled Go function pointer; the
specific IOCP and Winsock callback paths above have fixed trampolines, while
vectored exception handling still returns deterministic
`ERROR_CALL_NOT_IMPLEMENTED`. OS-delivered C signals are partial. Native `.pyd`
loading is unsupported: optional DLL symbol probes take CPython's fallback
paths, while the standard library and built-in extension modules are linked
into the generated Go package. Several CPython regression tests also require
optional built-in test extensions (`_ctypes`, `_multiprocessing`, `_testcapi`,
or `_testconsole`) that are not part of the current generated archive.
