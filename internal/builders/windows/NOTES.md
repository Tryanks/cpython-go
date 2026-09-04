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

## Verified outputs

- `tmp/windows_amd64/build/libpython3.14.a`: 234 members; contains
  `posixmodule.o` and `_winapi.o`; inspected member format `pe-x86-64`.
- `tmp/windows_arm64/build/libpython3.14.a`: 234 members; contains
  `posixmodule.o` and `_winapi.o`; inspected member header `COFF-ARM64`,
  machine `IMAGE_FILE_MACHINE_ARM64`.
