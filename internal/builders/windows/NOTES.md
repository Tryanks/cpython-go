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

- The broad CPython batch exposed Go stack exhaustion in deeply recursive C
  paths (`test_json`, `test_copy`, and `test_ast`). Windows initialized
  CPython's recursion guard with native OS-thread addresses while
  `_ccgo_frame_address` reports positions in the virtual modernc TLS stack;
  those disjoint ranges disabled the guard. `_GetCurrentThreadStackLimits`
  now publishes the matching virtual 8 MiB range, with a deep-JSON CI smoke.
- The next complete batch showed that 26 apparent module crashes shared one
  child-process startup bug in modernc's `Xwcsncmp`: `-X faulthandler` compares
  a four-code-unit option with a 12-code-unit prefix and modernc sliced both
  strings to 12. The routed comparison follows C bounds semantics. The batch's
  only remaining `TODOTODO` was modernc's `%c` scanf conversion in CPython's
  fixed Bluetooth-address parser; that sole `sscanf` call is routed as well.

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
- Run 33954290777: the first network probe imported far enough to hit
  modernc's `Xungetc` TODO in the tokenizer. The Windows route now implements
  the required one-byte pushback by rewinding modernc's unbuffered stream.
- Run 33955118375: name resolution, synchronous loopback TCP echo, Winsock
  `select`, `socketpair()`, and `asyncio.sleep(0)` passed on amd64 and arm64.
  The Proactor server then tried to invoke the native `AcceptEx` address as a
  ccgo Go function pointer and faulted at address `-1`.
- The `overlapped.c` patch now uses direct `ccgo_AcceptEx`,
  `ccgo_ConnectEx`, `ccgo_DisconnectEx`, and `ccgo_TransmitFile` declarations
  only under `MS_WINDOWS && CCGO && __CCGO__`; the ordinary native helper build
  retains CPython's original extension-pointer discovery. With no other Docker
  container active, both generated architectures were refreshed from this
  worktree using `run.sh --ccgo amd64` and then
  `WINDOWS_BUILDER_SKIP_BUILD=1 run.sh --ccgo arm64`.
- Run 33957766878 crossed the extension bridge and completed
  `asyncio.sleep(0)`, then exposed modernc's `XGetOverlappedResult` TODO when
  the accept completed. Routing that call through `x/sys/windows` completed
  the IOCP lifecycle.
- Run 33957987714 passed on Windows amd64, Windows arm64, Linux amd64, Linux
  arm64, and macOS. Both Windows jobs completed the full network smoke:
  resolver, TCP/select, socketpair, Proactor TCP echo, and local HTTP GET.
- After merging the POSIX routing work from `main`, run 33958539480 exposed a
  latent arm64 startup fault: modernc's `_wenviron` array lacks a terminating
  null pointer. Routing `__p__wenviron` to explicitly terminated stable UTF-16
  storage and synchronizing `_wgetenv`/`_wputenv` with Go's live environment
  fixed the out-of-bounds `wcschr` walk. Run 33958794432 then passed all five
  Windows, Linux, and macOS jobs at the merged head.
- A function-body audit of every remaining generated `libc.X*` call found one
  additional real modernc TODO, `Xdup2`. It is routed through a handle and
  private-descriptor-table implementation and has a required CI smoke covering
  replacement of an existing descriptor. `Xfileno` is the only textual audit
  match left and has a concrete Windows body.
- Batch run 33957766857 showed that compiler `__builtin_snprintf` and MinGW
  `__mingw_vsnprintf` aliases still reached modernc's formatter indirectly and
  triggered its unsupported-conversion TODO. Both aliases now use the routed
  Windows formatter too; the same artifacts confirmed the later registry and
  `fdopen` routes were needed and identified stack-overflow and compatibility
  failures separately from Go TODOs.
- `.github/workflows/windows-tests.yml` checks out CPython v3.14.7, embeds its
  test library, builds with `cpython_test`, and runs the selected 71 modules
  one process at a time on both Windows runners. The wrapper records test
  count/outcome/crash/timeout, emits a Markdown job summary, and uploads every
  module log.

## September 2026 Windows batch polish

The starting point was run 33962400678: 38 of 71 selected modules were OK on
both amd64 and arm64, with no Go panic. The failures that looked broad were
mostly four shared runtime seams: Win32 LastError was being lost, wide CRT
descriptors used narrow-path semantics, locale/time calls mixed Go and UCRT
state, and unsupported signal input entered UCRT's invalid-parameter fail-fast
handler.

### CI feedback loop

- Commits `4a29d2d` and `192af3f` added an optional comma-separated `tests`
  workflow-dispatch input, qualified module/class/method selection in
  `test_batch.go`, a 300-second per-target timeout, explicit closed stdin, and
  safe selector log names. They also corrected the workflow to build the
  current `stdlib/python314_tests.zip` format. Normal CI runs 33971443313 and
  33971779041 passed.
- Every test target still runs in its own process. A timeout kills the complete
  process tree with `taskkill /T /F`; summaries and complete logs remain
  per-architecture artifacts.

### Root causes and targeted validation

- Commit `dcdb9d3` gave routed file APIs an independent LastError channel.
  Modernc stored many Win32 errors only in CRT errno, while CPython clears
  errno before reading LastError. Run 33971998793 changed `test_pathlib` from
  15 errors to OK, `test_os` from 18 failures/26 errors to 11 failures/21
  errors, `test_shutil` from 6 errors to 3, and `test_codecs` from 8 errors to
  1 on both architectures.
- Commit `22f358a` delegated locale state and byte classification to UCRT while
  retaining stable copied locale strings for CPython startup and restore.
  Run 33972400158 made `test_re`, `test_pickle`, `test_locale`, and
  `test_decimal` pass on both architectures. `test_time` was reduced to one
  failure and one error; `test_socket` retained one missing-extension error.
- Commit `cd17799` replaced the wide-open path with direct `CreateFileW`, added
  UCRT-compatible Win32-to-CRT error mapping, tracked descriptor text/binary
  state, filtered hidden drive environment entries, and normalized one layer
  of syntactic spawn quoting. Run 33973019517 made `test_shutil`,
  `test_tempfile`, `test_zipfile`, `test_importlib`, and `test_zipimport` pass
  on both architectures. `test_os` retained only three tests importing absent
  `_ctypes`; `test_codecs` retained only its `_ctypes` code-page probe.
- Signal class run 33973021359 localized exit `0xc0000409` to
  `RaiseSignalTest`. Method run 33973166248 proved that only
  `RaiseSignalTest.test_invalid_argument` crashed on either architecture;
  handler, SIGINT, and `_thread.interrupt_main` cases passed. Commit `d336473`
  validates supported Windows signal numbers before calling UCRT. Run
  33973388386 then passed all 57 applicable `test_signal` tests on both
  architectures.
- The mmap failures were stale-error false positives: CPython reads LastError
  after successful `CreateFileMappingW`, but the prior wrapper left errno 2 or
  1224 behind. The routed wrapper overwrites both error channels on every call.
  Run 33973388386 passed all 51 applicable `test_mmap` tests on both
  architectures.
- `test_time` mixed Go timezone names/ranges with UCRT globals. Routing
  `tzset`, 64-bit `mktime`, `localtime`/`gmtime`, and finally `strftime` to
  UCRT made run 33973636346 pass all 64 applicable tests on amd64 and arm64.
- Asyncio submodule run 33973670388 showed that no individual submodule hung:
  all 34 selectors completed in about two minutes per architecture. The
  process-wait failures and leaked-handle warnings all started at the deliberate
  `_RegisterWaitForSingleObject` `ERROR_CALL_NOT_IMPLEMENTED` stub. Commit
  `9647460` replaces it with a fixed native Go trampoline and opaque-token
  dispatch to the translated one-shot callback, using callback-local libc TLS.
  Run 33974289225 passed the three formerly failing submodules and the complete
  2,509-test `test_asyncio` package on both architectures (2m08s amd64, 2m20s
  arm64).
- Budget run 33973417556 confirmed the 300-second limit. `test_statistics`
  passed on both architectures; `test_hashlib` passed in 3m32s on amd64 but
  still exceeded five minutes on arm64; `test_math` completed with ordinary
  libm-accuracy failures. `test_io` still exceeded five minutes without
  emitting a result. Qualified-class diagnostic run 33974733196 is not valid
  timing evidence for `test_io`: direct class loading bypasses that module's
  `load_tests` hook and its namespace injection, producing immediate fixture
  `AttributeError`s. A future bisection must load the module first and filter
  the resulting suite. The former `test_builtin` hang entered pdb at
  `TestBreakpoint.test_envar_good_path_other`: wide `_wputenv` updated the live
  process, but modernc's narrow `getenv` read a stale startup cache and ignored
  `PYTHONBREAKPOINT=sys.exit`. Commit `a98943e` routes narrow `getenv` to the
  same live environment. Run 33974657928 passed all 12 breakpoint tests and
  the complete 147-test `test_builtin` module on both architectures.

### Routed and implemented surface

The sorted Windows route list gained `CreateDirectoryW`, `CreateFileMappingW`,
`CreateFileW`, `DeleteFileW`, `FindClose`, `FindFirstFileW`, `FindNextFileW`,
`GetFileAttributesExW`, `GetFileAttributesW`, `GetFileInformationByHandle`,
`GetFileType`, `GetLastError`, `MultiByteToWideChar`, `RemoveDirectoryW`,
`SetFileAttributesW`, `WideCharToMultiByte`, `_commit`, `_lseeki64`, `_setmode`,
`_wstat64i32`, `close`, `getenv`, `isalnum`, `lseek`, `mktime`, `open`, `read`,
`strftime`, `tolower`, `toupper`, `tzset`, and `write`.

The matching calls were rewritten in every amd64 and arm64 shard. `_commit`
also appears as a function value, so that reference was rewritten explicitly
on both architectures. No CPython source/configuration input changed and no
Docker regeneration was needed for these routing-only edits; both generated
architectures were cross-compiled after each route group.

### Full batch comparison

Run 33974641629 tested commit `a98943e` with the 300-second limit. It finished
with 55 of 71 modules OK on both architectures, up from 38 of 71 on each in
run 33962400678. Seventeen modules became OK on each architecture and no
previously passing module regressed. The workflow conclusion is failure by
design whenever any table row is not OK; both jobs built, ran all 71 targets,
published the table, and uploaded their artifacts.

| Module | amd64 before | amd64 after | arm64 before | arm64 after |
|---|---|---|---|---|
| `test_int` | OK (skipped=5) | OK (skipped=5) | OK (skipped=5) | OK (skipped=5) |
| `test_dict` | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) |
| `test_list` | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) |
| `test_grammar` | OK | OK | OK | OK |
| `test_json` | OK (skipped=3) | OK (skipped=3) | OK (skipped=3) | OK (skipped=3) |
| `test_re` | FAILED (failures=1, skipped=4) | OK (skipped=4) | FAILED (failures=1, skipped=4) | OK (skipped=4) |
| `test_string` | OK | OK | OK | OK |
| `test_float` | OK (skipped=2) | OK (skipped=1) | OK (skipped=2) | OK (skipped=1) |
| `test_math` | TIMEOUT | FAILED (failures=2) | TIMEOUT | FAILED (failures=1, skipped=1) |
| `test_set` | OK | OK | OK | OK |
| `test_tuple` | OK | OK | OK | OK |
| `test_itertools` | OK (skipped=8) | OK (skipped=8) | OK (skipped=8) | OK (skipped=8) |
| `test_functools` | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) |
| `test_collections` | OK | OK | OK | OK |
| `test_datetime` | OK (skipped=108) | OK (skipped=120) | OK (skipped=108) | OK (skipped=120) |
| `test_os` | FAILED (failures=18, errors=26, skipped=137) | FAILED (errors=3, skipped=138) | FAILED (failures=17, errors=26, skipped=137) | FAILED (errors=3, skipped=137) |
| `test_io` | TIMEOUT | TIMEOUT | TIMEOUT | CRASH: exit status 1 |
| `test_pickle` | FAILED (errors=1, skipped=62) | OK (skipped=52) | FAILED (errors=1, skipped=62) | OK (skipped=52) |
| `test_struct` | OK (skipped=2) | OK (skipped=2) | OK (skipped=2) | OK (skipped=2) |
| `test_time` | FAILED (failures=1, errors=1, skipped=25) | OK (skipped=24) | FAILED (failures=1, errors=1, skipped=25) | OK (skipped=24) |
| `test_hashlib` | TIMEOUT | OK (skipped=5) | TIMEOUT | TIMEOUT |
| `test_random` | FAILED (errors=1, skipped=3) | FAILED (errors=1, skipped=3) | FAILED (errors=1, skipped=3) | FAILED (errors=1, skipped=3) |
| `test_fractions` | OK | OK | OK | OK |
| `test_enum` | OK (skipped=4) | OK (skipped=4) | OK (skipped=4) | OK (skipped=4) |
| `test_dataclasses` | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) |
| `test_typing` | OK | OK | OK | OK |
| `test_subprocess` | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) |
| `test_sys` | FAILED (failures=3, skipped=36) | FAILED (failures=3, skipped=36) | FAILED (failures=3, skipped=36) | FAILED (failures=3, skipped=36) |
| `test_builtin` | TIMEOUT | OK (skipped=7) | TIMEOUT | OK (skipped=7) |
| `test_exceptions` | OK (skipped=12) | OK (skipped=12) | OK (skipped=12) | OK (skipped=12) |
| `test_generators` | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) | OK (skipped=1) |
| `test_gc` | OK (skipped=8) | OK (skipped=8) | OK (skipped=8) | OK (skipped=8) |
| `test_weakref` | OK (skipped=2) | OK (skipped=2) | OK (skipped=2) | OK (skipped=2) |
| `test_operator` | OK | OK | OK | OK |
| `test_copy` | OK | OK | OK | OK |
| `test_argparse` | OK | OK | OK | OK |
| `test_logging` | FAILED (errors=5, skipped=21) | FAILED (errors=4, skipped=20) | FAILED (errors=5, skipped=21) | FAILED (errors=4, skipped=20) |
| `test_csv` | OK (skipped=4) | OK (skipped=4) | OK (skipped=4) | OK (skipped=4) |
| `test_codecs` | FAILED (errors=8, skipped=13) | FAILED (errors=1, skipped=11) | FAILED (errors=8, skipped=13) | FAILED (errors=1, skipped=11) |
| `test_unittest` | FAILED (failures=1, skipped=50) | FAILED (failures=1, skipped=50) | FAILED (failures=1, skipped=50) | FAILED (failures=1, skipped=50) |
| `test_sort` | OK | OK | OK | OK |
| `test_long` | OK | OK | OK | OK |
| `test_array` | OK (skipped=43) | OK (skipped=43) | OK (skipped=43) | OK (skipped=43) |
| `test_memoryview` | OK (skipped=20) | OK (skipped=20) | OK (skipped=20) | OK (skipped=20) |
| `test_zipimport` | FAILED (errors=1, skipped=43) | OK (skipped=4) | FAILED (errors=1, skipped=43) | OK (skipped=4) |
| `test_importlib` | FAILED (errors=12, skipped=55) | OK (skipped=38) | FAILED (errors=12, skipped=55) | OK (skipped=38) |
| `test_locale` | FAILED (errors=1, skipped=9) | OK (skipped=1) | FAILED (errors=1, skipped=9) | OK (skipped=1) |
| `test_select` | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) |
| `test_mmap` | FAILED (errors=4, skipped=7) | OK (skipped=7) | TIMEOUT | OK (skipped=7) |
| `test_threading` | FAILED (failures=1, skipped=28) | FAILED (failures=1, skipped=28) | FAILED (failures=1, skipped=28) | FAILED (failures=1, skipped=28) |
| `test_socket` | FAILED (errors=2, skipped=510) | FAILED (errors=1, skipped=508) | FAILED (errors=2, skipped=510) | FAILED (errors=1, skipped=508) |
| `test_signal` | CRASH: exit status 0xc0000409 | OK (skipped=43) | CRASH: exit status 0xc0000409 | OK (skipped=43) |
| `test_asyncio` | TIMEOUT | OK (skipped=169) | TIMEOUT | OK (skipped=169) |
| `test_statistics` | OK | OK | TIMEOUT | OK |
| `test_decimal` | FAILED (errors=1, skipped=198) | OK (skipped=199) | FAILED (errors=1, skipped=198) | OK (skipped=199) |
| `test_pathlib` | FAILED (errors=15, skipped=387) | OK (skipped=387) | FAILED (errors=15, skipped=387) | OK (skipped=387) |
| `test_shutil` | FAILED (errors=6, skipped=69) | OK (skipped=57) | FAILED (errors=6, skipped=69) | OK (skipped=57) |
| `test_tempfile` | FAILED (failures=5, errors=7, skipped=3) | OK (skipped=3) | FAILED (failures=5, errors=7, skipped=3) | OK (skipped=3) |
| `test_zipfile` | FAILED (errors=3, skipped=155) | OK (skipped=102) | FAILED (errors=3, skipped=155) | OK (skipped=102) |
| `test_email` | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) |
| `test_xml_etree` | OK (skipped=5) | OK (skipped=5) | OK (skipped=5) | OK (skipped=5) |
| `test_queue` | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) | OK (skipped=6) |
| `test_inspect` | FAILED (failures=1, errors=1, skipped=8) | FAILED (failures=1, errors=1, skipped=8) | FAILED (failures=1, errors=1, skipped=8) | FAILED (failures=1, errors=1, skipped=8) |
| `test_ast` | FAILED (failures=1, skipped=1) | FAILED (failures=1, skipped=1) | FAILED (failures=1, skipped=1) | FAILED (failures=1, skipped=1) |
| `test_coroutines` | OK (skipped=3) | OK (skipped=3) | OK (skipped=3) | OK (skipped=3) |
| `test_uuid` | OK (skipped=73) | OK (skipped=73) | OK (skipped=73) | OK (skipped=73) |
| `test_winapi` | FAILED (errors=1) | OK | FAILED (errors=1) | OK |
| `test_winreg` | FAILED (errors=1, skipped=4) | FAILED (errors=1, skipped=4) | OK (skipped=7) | OK (skipped=7) |
| `test_winconsoleio` | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) |
| `test_winsound` | OK | OK | OK | OK |
| `test_msvcrt` | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) | FAILED (errors=1) |

The remaining failures group cleanly:

- absent optional built-ins account for `_ctypes` in `test_os`,
  `test_subprocess`, `test_codecs`, and `test_msvcrt`; `_multiprocessing` in
  `test_logging` and `test_socket`; `_testcapi` in `test_threading`; and
  `_testconsole` in `test_winconsoleio`;
- resource/budget behavior accounts for `test_io` (amd64 timeout; arm64
  `MemoryError` while formatting its first underlying error) and the arm64-only
  `test_hashlib` timeout;
- Go/libm numerical fidelity accounts for `test_math` and the mocked boundary
  path in `test_random.gammavariate`;
- packaged-stdlib/build-mode expectations account for `test_sys`,
  `test_unittest`, and `test_inspect`; and
- the deep AST recursion expectation and amd64 registry-reflection API remain
  in `test_ast` and `test_winreg`, respectively. Arm64 skips the reflection
  coverage and passes `test_winreg`.
