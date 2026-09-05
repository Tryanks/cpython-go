# Windows compatibility follow-up

This file tracks work intentionally left after the September 2026
`windows-polish` CPython 3.14.7 batch. Exact before/after results and CI run
links are recorded in `NOTES.md`; implemented runtime contracts are in
`RUNTIME.md`.

## Optional extension coverage

Several remaining regression-test failures are import failures rather than
Windows ABI defects. Decide whether to add these modules to the generated
archive or skip their dependent tests explicitly:

- `_ctypes`: the only remaining errors in targeted `test_os` and
  `test_codecs`, plus failures in `test_msvcrt` and some subprocess coverage;
- `_multiprocessing`: process helpers used by `test_socket` and
  `test_logging`;
- `_testcapi`: threading and implementation-detail regression tests; and
- `_testconsole`: `test_winconsoleio` fixtures.

## Core compatibility not addressed in this round

- `test_random`: investigate the `gammavariate` mocked-random
  `StopIteration`; this is numerical/control-flow fidelity, not a Win32 call.
- `test_math`: improve libm fidelity for subnormal logarithms/gamma and a few
  boundary values; the suite now completes instead of timing out.
- `test_io`: direct qualified-class selection bypasses this module's custom
  `load_tests` injection and produces invalid fixture errors. Bisect by loading
  the whole module and applying unittest `-k` filters, or add verbose module
  instrumentation, before changing its budget again. In final run 33974641629,
  amd64 reached the five-minute timeout without output; arm64 exited after
  3m23s with `MemoryError` while unittest was formatting the first underlying
  error, so that traceback does not yet identify the failing test.
- `test_hashlib`: amd64 passes in roughly 3.5 minutes, while arm64 still
  exceeds the explicit five-minute module budget; profile or split by class.
- `test_sys`: reconcile packaged-stdlib path reporting and CPython build-mode
  expectations for GIL/JIT probes.
- `test_unittest`: make subprocess discovery robust when the checkout and
  temporary directory are on different Windows drives.
- `test_inspect`: define source/origin behavior for modules loaded from the
  embedded standard-library archive while tests come from a checkout.
- `test_ast`: investigate recursion accounting for the 500,000-term parser
  stress case without weakening the generated-C recursion guard.
- `test_winreg`: implement registry reflection APIs where the OS supports
  them; arm64 currently skips this coverage.

## Explicit runtime ceilings

- Native `.pyd` loading remains unsupported; extension modules must be linked
  into the generated Go package.
- OS-delivered signal-handler callbacks remain partial. Routed `raise()` and
  validation are covered, but arbitrary native signal delivery is not.
- Vectored exception handlers still require a purpose-built native callback
  trampoline. Do not pass a raw ccgo function value to kernel32.
- Audit and deepen less-used CRT wide-locale operations (`_wcsftime`,
  multibyte conversion outside the tested UTF-8 paths) as broader locale
  suites are enabled.

## Builder hygiene

- Keep `shimmedLibc["windows"]` sorted and apply every route to both generated
  architectures.
- Regenerate only with `internal/builders/windows/run.sh --ccgo <arch>
  /tmp/cpython-3.14.7`, from the repository root, one container at a time, and
  only while `docker ps` is empty.
