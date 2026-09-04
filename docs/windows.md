# Windows targets (windows/amd64, windows/arm64) — plan

Findings (research, 2026-09-05):

- cznic precedent: libquickjs cross-generates windows from Linux with
  llvm-mingw (GNU mingw-w64 is rejected: ccgo cannot type-check its
  `_mingw.h` `__int128` use), `--goos windows --goarch amd64 --cpp <clang>`,
  `-map gcc=...,ar=...`, `-mlong-double-64`, and shares the amd64 output with
  arm64 via a `windows && (amd64 || arm64)` build line. ccgo's `-winapi`
  generates DLL trampolines from selected headers; `-winabi` is dead code.
  modernc.org/libc ships no Windows headers; the mingw sysroot provides them.
  libc's windows layer is thin (hand-written, x/sys/windows based).
- CPython has no upstream mingw support. MSYS2 maintains a 103-patch port
  for 3.14.7 (https://github.com/msys2-contrib/cpython-mingw, branch
  mingw-v3.14.7). It is validated for native MSYS2 builds, not for cross
  builds from Linux/macOS; cross-configure needs `--with-build-python` (a
  build-host Python 3.14) and a CONFIG_SITE with precomputed `ac_cv_*`.
- The MSVC/PCbuild layout is not usable with ccgo (cl.exe/MSBuild, MSVC
  extensions such as `__try`).
- Win32 API gap in modernc.org/libc for CPython's Windows code: ~70 of ~150
  sampled calls missing; expect 90–130 after a full link, 2–4k lines of
  supplement (files/paths/volumes, processes, TLS/synchronisation, named
  pipes/overlapped I/O, console/locale, Winsock, security/RPC).

Plan:

1. Builder container (linux/arm64, llvm-mingw aarch64-host bundle, Linux
   Python 3.14, autotools, Go) — `internal/builders/windows/`.
2. Plain cross build of `libpython3.14.a` with the MSYS2 patch set, amd64
   then arm64; CONFIG_SITE per arch; NOTES.md of every workaround.
3. `ccgo -exec make` for amd64; fix cc/v4 issues on the Windows units.
4. Link, shard, typecheck; write `libpython/libc_windows.go` from the
   undefined-symbol list; first acceptance `print(45)` on Windows.
5. Behaviour on amd64 (os/pathlib/io/threading/subprocess/socket tests),
   then arm64.

Estimate: 3–5 weeks to an amd64 boot, 6–10 weeks for both architectures.
A Windows host (GitHub Actions runner or a Windows 11 ARM64 VM) is needed
from step 2 on to run the produced binaries.
