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

Run 33957987714 passed this suite on Windows amd64 and arm64 together with the
Linux amd64, Linux arm64, and macOS jobs.

The broader `.github/workflows/windows-tests.yml` runs on pushes to
`windows-*` branches and by manual dispatch. It checks out CPython v3.14.7,
builds `stdlib/python314_tests.zip.gz`, builds the interpreter with the
`cpython_test` tag, and runs the selected CPython `test.*` modules in separate
processes. Each module has a 90-second limit; the workflow writes a Markdown
table to the job summary and uploads the table plus complete logs as an
artifact for each architecture.

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

Native APIs that require callbacks cannot consume a transpiled Go function
pointer. Optional wait registration and vectored exception handling therefore
return deterministic `ERROR_CALL_NOT_IMPLEMENTED` results. OS-delivered C
signals are also partial. Native `.pyd` loading is unsupported: optional DLL
symbol probes take CPython's fallback paths, while the standard library and
built-in extension modules are linked into the generated Go package.
