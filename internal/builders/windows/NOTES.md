# Windows cross-build notes

## Pinned inputs

- Builder platform: `linux/arm64`.
- Base images: `golang:1.27.0-bookworm` and
  `python:3.14.7-slim-bookworm` (exact version tags).
- llvm-mingw: release `20260826` (LLVM 23.1.0), UCRT aarch64 Linux-host
  bundle `llvm-mingw-20260826-ucrt-ubuntu-22.04-aarch64.tar.xz`, SHA-256
  `4eb475cccf5e5e37ea3b693a52227e70a86ae70abafceb9ecd83887e67699c9d`.
- CPython: tag `v3.14.7`, commit
  `823f0323ee6ec1402088b73bce1a38473cac36dc`.
- MSYS2 patch branch tip:
  `56cdb4b201d96f26cdcb1f7c2b93086298f7df11`.

## Configure cache policy

Each `config.site.<arch>` starts with all answers from the patched tree's
`Misc/config_mingw` and `Misc/cross_mingw32`. It also carries the cpython-go
policy answers from `generator.go`: no `dlopen`, zlib header, x87 or mc68881
asm, SSE/AVX HACL probing, and all external-library modules marked `n/a`.

Additional cross-only `ac_cv_*` answers discovered during this build are
listed below as they are added.

The supplied answers are:

```text
ac_cv_func_ftruncate=ignore
ac_cv_func_truncate=ignore
ac_cv_func_alarm=ignore
ac_cv_file__dev_ptmx=ignore
ac_cv_file__dev_ptc=no
ac_cv_func_getpeername=yes
ac_cv_little_endian_double=yes
ac_cv_big_endian_double=no
ac_cv_mixed_endian_double=no
ac_cv_tanh_preserves_zero_sign=yes
ac_cv_wchar_t_signed=no
ac_cv_have_size_t_format=no
ac_cv_func_dlopen=no
ac_cv_lib_dl_dlopen=no
ac_cv_header_zlib_h=no
ac_cv_gcc_asm_for_x87=no
ac_cv_gcc_asm_for_mc68881=no
```

The two `ax_cv_check_cflags_*` answers suppress x86 SSE/AVX2 probes. Module
policy is expressed separately with the `py_cv_module_*=n/a` entries in each
site file.

## Patch handling

`internal/patch/cpython-3.14.diff` is applied first with GNU patch in
best-effort mode. Successful hunks are retained; rejected hunks are emitted
to `tmp/windows_<arch>/cpython-3.14.diff.log`. This is intentional for the
plain-C milestone because these changes are ccgo workarounds. The MSYS2 diff
is then required to apply completely to the exact v3.14.7 source tree.

Observed rejected ccgo patch hunks: none. All hunks, including the later
ccgo-critical pyexpat, pickle, parser, importdl, ceval, blake2, and hmac
changes, applied to v3.14.7 before the MSYS2 patch.

## Configure and make workarounds

- No additional configure-time cross answers were requested for amd64. The
  defaults from `Misc/config_mingw`, `Misc/cross_mingw32`, and the project
  policy cache were sufficient.
- `_wmi` is set to `n/a`. Its MSYS2-added C++ compile rule inherits
  `PY_CFLAGS`, including `-std=c11`; llvm-mingw 20260826's clang++ rejects
  that C-only language standard. `_wmi` is not needed for the static-library
  milestone; `_winapi` remains enabled and is included in the archive.
- Configure's patched MinGW path conversion tries to canonicalize the
  default sentinel prefix `NONE`, producing a harmless `cd: NONE` diagnostic.
  Passing the conventional `--prefix=/usr/local` avoids that diagnostic.
- A copied git worktree has a `.git` indirection to metadata outside the
  container mount. The make invocation overrides `GITVERSION`, `GITTAG`, and
  `GITBRANCH` with the no-op `:` command so `Modules/getbuildinfo.o` is
  deterministic and does not try to follow that unavailable host path.

## Future ccgo / host-tool blockers

Both amd64 and arm64 `libpython3.14.a` builds complete with 234 members, so
there is no remaining plain-C arm64 blocker. The arm64 member inspected is an
`IMAGE_FILE_MACHINE_ARM64` (`COFF-ARM64`) object; llvm-objdump's compatibility
format label for it is the historical `pe-arm-wince`.

The `libpython3.14.a` target does not need to execute a target Windows binary.
Broader Makefile targets still do: `python.exe` itself is PE, and targets such
as PGO, frozenmain tests, stable-ABI generation, test/pythoninfo, install-time
queries, and several maintenance targets invoke `./$(BUILDPYTHON)`. The
single-platform freeze path also links `Programs/_freeze_module` and
`_bootstrap_python`; this cross configuration instead sets `FREEZE_MODULE`
to the native `Programs/_freeze_module.py` via `/usr/local/bin/python3.14`.
Later ccgo generation must keep that native `PYTHON_FOR_BUILD` path and avoid
or override every direct `./$(BUILDPYTHON)` rule.

The Windows generator deliberately skips `sysconfigdata()`: the cross-built
`python.exe` cannot run in the Linux builder. Windows `_sysconfigdata` must be
obtained by a different mechanism in a later milestone.

## ccgo generation

Run the amd64 transpilation with:

```sh
internal/builders/windows/run.sh --ccgo amd64 /tmp/cpython-3.14.7
```

The container mounts the repository and a persistent Windows Go module cache,
sets `TARGET_GOOS`, `TARGET_GOARCH`, `MINGW_TRIPLE`, `BUILD_TRIPLE`,
`BUILD_PYTHON`, and `CONFIG_SITE`, then runs `go run generator.go`. During make,
`CC=gcc AR=ar` select names ccgo can shim; ccgo maps them to llvm-mingw.

The successful amd64 run exposed these ccgo-specific failures, in order:

- Mapping `gcc` directly to the llvm-mingw executable was insufficient.
  `ccgo -exec` records the pre-shim `gcc` found on `PATH` in `CCGO_GCC`, which
  selected the Linux host compiler and mixed glibc declarations with the
  MinGW configuration (`gid_t`, `uid_t`, and `LONG_BIT` failures). The
  generator now prepends tiny `gcc` and `ar` wrappers for the cross tools and
  maps the shim names to those wrappers.
- cc/v4 could not parse LLVM 23's `ia32intrin.h` and `f16cintrin.h` inline
  bodies. Defining both `__INTRIN_H` and `__X86INTRIN_H` bypasses those bodies;
  CPython does not require them on the selected GCC-compatible code paths.
- mingw-w64 defines `ssize_t` but not the POSIX `SSIZE_MAX` or `PATH_MAX`
  macros used by these translation units. The ccgo invocation supplies
  `SSIZE_MAX=INTPTR_MAX` and `PATH_MAX=260`.
- cc/v4 rejected MSVC `_Pragma(section(...))` declarations in
  `pycore_debug_offsets.h`, reached from `pylifecycle.c` and
  `_asynciomodule.c`. The common patch omits only those section-placement
  pragmas under `MS_WINDOWS && CCGO`; the metadata object remains defined.
- cc/v4 rejected C23 declarations immediately following two `case` labels in
  `posixmodule.c`. Braces around those case bodies make their scopes explicit.
- Linking initially found duplicate `HV_GUID_*` globals from both
  `socketmodule` and `signalmodule`: `signalmodule.c` included all of
  `socketmodule.h` only to obtain `SOCKET_T`. Under `MS_WINDOWS && CCGO`, the
  patch includes `winsock2.h` and defines that one typedef instead.

The generated archive contains 234 `.o.go` members, exactly matching all 234
native members. The build tree contains 246 native `.o` and 246 `.o.go` files
when the six HACL archives and expat objects are included.

`ccMain` links `libpython3.14.ago`, all six `Modules/_hacl/*.ago` archives, and
`Modules/expat/libexpat.ago`. Postprocessing moved 1,461 variables (2,176,140
bytes) into the data blob and reduced Go source from 59,359,682 to 35,271,134
bytes. The result is 12 numbered Go shards, one data Go file, and one data
blob. The original type-check gaps are inventoried in `UNDEFINED.md`; the
Windows libc supplement now closes the startup and smoke-test subset described
below.

## Verified outputs

- `tmp/windows_amd64/build/libpython3.14.a`: 234 members; contains
  `posixmodule.o` and `_winapi.o`; inspected member format `pe-x86-64`.
- `tmp/windows_amd64/build/libpython3.14.ago`: 234 members; every native archive
  member has a matching transpiled member.
- `libpython/ccgo_windows_amd64_00.go` through `_11.go`,
  `ccgo_windows_amd64_data.go`, and `ccgo_windows_amd64_data.bin`: 37,447,274
  bytes total (35,271,134 bytes of Go plus a 2,176,140-byte data blob).
- `tmp/windows_arm64/build/libpython3.14.a`: 234 members; contains
  `posixmodule.o` and `_winapi.o`; inspected member header `COFF-ARM64`,
  machine `IMAGE_FILE_MACHINE_ARM64`.

## Windows runtime CI iterations

- Run 33949767829 (`main`): the executable built, then `print(45)` failed in
  `_PyPreConfig_Read` because modernc's Windows `Xsetlocale` always returned
  null; fatal reporting then hit modernc's `XOutputDebugStringW` TODO panic.
  Routed both calls and proactively routed the known-TODO `Xmbstowcs` plus
  matching `Xwcstombs` to deterministic UTF-8/UTF-16 shims.
- The concurrently running arm64 ccgo generation completed before the first
  runtime fix was committed. The same four routes were applied to its 12
  shards; both `GOOS=windows GOARCH=amd64` and `GOARCH=arm64` cross-builds
  passed locally.
- Run 33950181453: locale preinitialization passed. Path configuration then
  panicked while loading `pathcch.dll` for `_PathCchSkipRoot`. Replaced both
  PathCch wrappers with pure-Go UTF-16 path operations, and routed modernc's
  `_wopen` (which decodes a UTF-16 pointer as narrow bytes) and `_wgetenv`
  (whose cached environment drops an entry) before the next run.
- Run 33950390306: getpath completed, then
  `_PyPathConfig_UpdateGlobal` dereferenced null while appending the Windows
  path delimiter. modernc's `Xwcschr` returns null instead of the terminator
  address for `wcschr(text, L'\0')`; routed it to a correct UTF-16 scan.
- Run 33950554823: path publication completed and CPython reached the optional
  `GetFileInformationByName` probe, where modernc's `XLoadLibraryW` hit a TODO
  panic. Routed `LoadLibraryW` and the paired `FreeLibrary`; routed
  `GetProcAddress` to a deterministic unavailable result because a native
  `FARPROC` cannot be called as a ccgo Go function pointer. This makes optional
  API probes use CPython's supported fallback paths. Also corrected the routed
  `_wopen` shim to translate UCRT `_O_*` bits before calling modernc's
  `x/sys/windows`-backed descriptor implementation.
- Run 33950801035: `print(45)` passed for the first time. Importing `json`
  reached `re`/`enum`, but `RegexFlag(72)` failed because `_all_bits_` was a
  float. modernc defines Windows C `unsigned long` as `uint32` while its
  generic `X__builtin_clzl` calls `bits.LeadingZeros64`; CPython therefore
  calculated `(127).bit_length()` as `-29`, and `2 ** -29` took the float
  power path. Routed `__builtin_clzl` to `bits.LeadingZeros32`.
- Run 33951044241: both original Windows smoke commands passed: `print(45)`
  printed `45`, and the import command printed `win32 (3, 14) [1]`. This is
  the first confirmed working interpreter run on `windows-latest`; the CI job
  was expanded next to cover the requested modules and a small unittest.
- Run 33951207119: the original commands stayed green, then the expanded import
  list aborted in `_blake2` CPU detection. The existing CCGO patch disabled
  GCC `cpuid.h` assembly, but llvm-mingw also defines `_M_X64`, selecting the
  `__cpuidex` branch; ccgo lowers that intrinsic to an "assembler statements
  not supported" assertion. Both CPUID branches are now disabled specifically
  for `MS_WINDOWS && CCGO`, selecting the supported scalar hash paths.
- Run 33951423167: both original commands, the requested imports, and behavior
  checks for `os`, a real `threading.Thread`, SHA-256, pickle, regex, and
  `datetime` passed. The run was cancelled after `subprocess.check_output()`
  remained blocked for more than two minutes; all three non-Windows jobs had
  already passed.
- Run 33951727288: splitting subprocess coverage proved that process creation,
  a timed process wait, a bounded three-byte pipe read, and the small
  `unittest` all pass. The full run was green across Windows, macOS, Linux
  amd64, and Linux arm64. Only reading the pipe through EOF was broken.
- Run 33952028834: a raw-handle probe showed the original write end returned by
  `_winapi.CreatePipe` remained a valid pipe handle after the child exited and
  after `gc.collect()`; a daemon reader waiting for EOF was still blocked after
  five seconds. The Windows job's diagnostic assertion failed as intended
  (the workflow run itself remained successful because the Windows job was
  still nonblocking).
- Run 33952410898: the embedded `Lib/subprocess.py` now explicitly closes each
  original child-only pipe end after creating its inheritable duplicate.
  `subprocess.check_output()` consumed output through EOF, and every Windows
  startup, import, behavior, process, pipe, and unittest smoke passed. All four
  jobs in the workflow completed successfully.
- After run 33952410898, a clean
  `internal/builders/windows/run.sh --ccgo amd64 /tmp/cpython-3.14.7` replay
  applied every project and MSYS2 patch, produced matching 234-member native
  and `.ago` archives, and regenerated all 12 amd64 shards. The regenerated
  Blake2 detector contains scalar zero-valued feature inputs and no CPUID
  assertion calls. Both Windows architectures then cross-built locally.
- Run 33953203638: the cleanly regenerated amd64 shards retained every passing
  runtime smoke. Routed `CloseHandle` and modernc's TODO
  `SetHandleInformation`, then added an `os.pipe()` write/close/read/close
  round-trip; it passed together with pipe EOF, subprocess, and unittest. The
  Windows job was promoted from `continue-on-error` to a required CI job after
  this result.
