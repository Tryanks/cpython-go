// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

//go:build ignore

// Command generator transpiles CPython to Go using ccgo.
//
// Usage:
//
//	CPYTHON_SRC=/path/to/cpython-3.14 go run generator.go
//
// Steps:
//  1. Configure CPython natively in tmp/build (out-of-tree), static, no
//     dynamic loading, no external libraries.
//  2. Run `make libpython3.14.a` under ccgo -exec, so every cc/ar invocation
//     is shadowed: the real compiler builds native objects (needed for the
//     build-time helpers _freeze_module/_bootstrap_python) and ccgo emits a
//     .o.go beside each .o.
//  3. Link the .ago archives into libpython/ccgo_<goos>_<goarch>.go.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	cc "modernc.org/cc/v4"
	ccgo "modernc.org/ccgo/v4/lib"
	util "modernc.org/fileutil/ccgo"
)

const (
	pyVer   = "3.14"
	libName = "libpython" + pyVer + ".a"
	tmp     = "tmp"
	outDir  = "libpython"
)

var (
	ccArgs = []string{
		"--prefix-enumerator=E",
		"--prefix-external=x_",
		"--prefix-field=F",
		"--prefix-macro=M",
		"--prefix-static-internal=_",
		"--prefix-static-none=_",
		"--prefix-tagged-enum=_",
		"--prefix-tagged-struct=T",
		"--prefix-tagged-union=T",
		"--prefix-typename=T",
		"--prefix-undefined=_",
		// Marks ccgo-only code paths in internal/patch (see _posixsubprocess.c).
		"-DCCGO=1",
		"-DNDEBUG",
		// FLT_ROUNDS expands to builtins libc does not provide.
		"-D__builtin_flt_rounds()=1",
		// 3.14 measures C stack depth through the machine stack pointer;
		// transpiled code has no C stack, so route it to a virtual one kept
		// per libc.TLS in libpython/libc_darwin.go.
		"-D__builtin_frame_address(x)=ccgo_frame_address()",
		// libmpdec's ANSI implementation avoids x86 inline assembly and
		// __uint128_t, neither of which ccgo can transpile reliably.
		"-DANSI=1",
		"-U__SIZEOF_INT128__",
		"-eval-all-macros",
		"-extended-errors",
		// Modules/_blake2/impl/blake2-impl.h uses an empty asm memory barrier.
		"-ignore-asm-errors",
		"-ignore-link-errors",
		"-ignore-unsupported-alignment",
	}
	// configureEnv pins configure decisions that the host would otherwise get
	// wrong for a ccgo build: no dlopen (no dynamic extension modules), all
	// stdlib modules static, no pkg-config so no host libraries (libb2, zlib,
	// openssl, ...) leak into the build.
	configureEnv = []string{
		"MODULE_BUILDTYPE=static",
		// configure re-detects pkg-config from PATH, so PKG_CONFIG=false is not
		// enough; an empty .pc search path makes every module fall back to
		// its vendored copy (libb2, expat, mpdecimal...).
		"PKG_CONFIG_LIBDIR=/nonexistent",
		"PKG_CONFIG_PATH=",
		"ac_cv_func_dlopen=no",
		"ac_cv_lib_dl_dlopen=no",
		// Configure's zlib link probes use the platform's native libz. The
		// transpiled module is resolved to modernc.org/libz at ccgo link time.
		"ac_cv_lib_z_gzread=yes",
		"ac_cv_lib_z_inflateCopy=yes",
		// Likewise, configure's SQLite feature probes use the native library,
		// while the transpiled extension resolves to modernc.org/libsqlite3.
		// Seed the complete set from configure.ac so cross targets do not need
		// a target-native SQLite library just to enable the static module.
		"ac_cv_lib_sqlite3_sqlite3_bind_double=yes",
		"ac_cv_lib_sqlite3_sqlite3_column_decltype=yes",
		"ac_cv_lib_sqlite3_sqlite3_column_double=yes",
		"ac_cv_lib_sqlite3_sqlite3_complete=yes",
		"ac_cv_lib_sqlite3_sqlite3_progress_handler=yes",
		"ac_cv_lib_sqlite3_sqlite3_result_double=yes",
		"ac_cv_lib_sqlite3_sqlite3_set_authorizer=yes",
		"ac_cv_lib_sqlite3_sqlite3_trace_v2=yes",
		"ac_cv_lib_sqlite3_sqlite3_value_double=yes",
		"ac_cv_lib_sqlite3_sqlite3_load_extension=yes",
		"ac_cv_lib_sqlite3_sqlite3_serialize=yes",
		// x87 control-word inline asm (Python/pymath.c) cannot be transpiled.
		"ac_cv_gcc_asm_for_x87=no",
		"ac_cv_gcc_asm_for_mc68881=no",
		"ac_cv_gcc_asm_for_x64=no",
		"ac_cv_type___uint128_t=no",
		// HACL* SIMD (SSE/AVX2 intrinsics) cannot be transpiled.
		"ax_cv_check_cflags__Werror__mavx2=no",
		"ax_cv_check_cflags__Werror__msse__msse2__msse3__msse4_1__msse4_2=no",
		"py_cv_module__crypt=n/a",
		"py_cv_module__multiprocessing=n/a",
		"py_cv_module__posixshmem=n/a",
		"py_cv_module_syslog=n/a",
		"py_cv_module__bz2=n/a",
		"py_cv_module__ctypes=n/a",
		"py_cv_module__curses=n/a",
		"py_cv_module__curses_panel=n/a",
		"py_cv_module__dbm=n/a",
		"py_cv_module__gdbm=n/a",
		"py_cv_module__hashlib=n/a",
		"py_cv_module__lzma=n/a",
		"py_cv_module__scproxy=n/a",
		"py_cv_module__ssl=n/a",
		"py_cv_module__tkinter=n/a",
		"py_cv_module__uuid=n/a",
		"py_cv_module_nis=n/a",
		"py_cv_module_readline=n/a",
		"py_cv_module__zstd=n/a",
	}
	// configureEnvOS: functions modernc.org/libc lacks on a platform; CPython
	// has fallbacks for all of them.
	configureEnvOS = map[string][]string{
		"darwin": {
			"ac_cv_func_faccessat=no",
			"ac_cv_func_fchmodat=no",
			"ac_cv_func_fchownat=no",
			"ac_cv_func_fdopendir=no",
			"ac_cv_func_fstatat=no",
			"ac_cv_func_futimens=no",
			"ac_cv_func_getloadavg=no",
			"ac_cv_func_kqueue=no",
			"ac_cv_func_lchmod=no",
			"ac_cv_func_linkat=no",
			"ac_cv_func_mkdirat=no",
			"ac_cv_func_mkfifoat=no",
			"ac_cv_func_mknodat=no",
			"ac_cv_func_openat=no",
			"ac_cv_func_posix_spawn=no",
			"ac_cv_func_posix_spawnp=no",
			"ac_cv_func_readlinkat=no",
			"ac_cv_func_renameat=no",
			"ac_cv_func_sigaltstack=no",
			"ac_cv_func_symlinkat=no",
			"ac_cv_func_unlinkat=no",
			"ac_cv_func_utimensat=no",
			"ac_cv_func_waitid=no",
			"ac_cv_func_posix_openpt=no",
			"ac_cv_func_grantpt=no",
			"ac_cv_func_unlockpt=no",
			"ac_cv_func_ptsname=no",
			"ac_cv_func_ptsname_r=no",
			"ac_cv_func_pthread_getname_np=no",
			"ac_cv_func_pthread_setname_np=no",
			"ac_cv_func_getlogin_r=no",
		},
		"linux": {
			// transpiled musl: no semaphores, scheduler, condattr clocks, ...
			"ac_cv_func_forkpty=no",
			"ac_cv_func_posix_spawn=no",
			"ac_cv_func_posix_spawnp=no",
			"ac_cv_func_pthread_condattr_setclock=no",
			"ac_cv_func_pthread_getname_np=no",
			"ac_cv_func_pthread_setname_np=no",
			"ac_cv_func_pthread_getcpuclockid=no",
			"ac_cv_func_pthread_kill=no",
			"ac_cv_func_sched_get_priority_max=no",
			"ac_cv_func_sched_getaffinity=no",
			"ac_cv_func_sched_getparam=no",
			"ac_cv_func_sched_getscheduler=no",
			"ac_cv_func_sched_rr_get_interval=no",
			"ac_cv_func_sched_setaffinity=no",
			"ac_cv_func_sched_setparam=no",
			"ac_cv_func_sched_setscheduler=no",
			"ac_cv_posix_semaphores_enabled=no",
		},
	}
	configureArgs = []string{
		"--enable-ipv6",
		"--disable-shared",
		"--disable-test-modules",
		"--with-static-libpython",
		"--with-system-libmpdec=no",
		"--without-computed-gotos",
		"--without-ensurepip",
		"--without-mimalloc",
		// sys.remote_exec needs mach_vm_* / proc_regionfilename; not portable.
		"--without-remote-debug",
		"--without-pymalloc",
	}
	dev    = os.Getenv("GO_GENERATE_DEV") != ""
	goarch = env("TARGET_GOARCH", env("GOARCH", runtime.GOARCH))
	goos   = env("TARGET_GOOS", env("GOOS", runtime.GOOS))
	gsed   = "sed"
	j      = strconv.Itoa(runtime.GOMAXPROCS(-1))
	target = fmt.Sprintf("%s/%s", goos, goarch)
	// Per-target scratch dirs so several targets can be generated side by side.
	build   = filepath.Join(tmp, goos+"_"+goarch, "build")
	srcCopy = filepath.Join(tmp, goos+"_"+goarch, "cpython")
)

func env(name, deflt string) (r string) {
	r = deflt
	if s := os.Getenv(name); s != "" {
		r = s
	}
	return r
}

func fail(rc int, msg string, args ...any) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(fmt.Sprintf("FAIL: "+msg, args...)))
	os.Exit(rc)
}

func main() {
	if ccgo.IsExecEnv() {
		// Acting as the cc/ar shim inside `make`. Errors are reported but
		// never fatal: the native build must go on so the build-time helpers
		// get linked; missing .o.go files surface at the final link.
		if err := ccgo.NewTask(goos, goarch, os.Args, os.Stdout, os.Stderr, nil).Main(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}

	src, err := filepath.Abs(srcCopy)
	if err != nil {
		fail(1, "%v", err)
	}

	if s := cc.LongDouble64Flag(goos, goarch); s != "" {
		ccArgs = append(ccArgs, s)
	}
	if dev {
		ccArgs = append(ccArgs, "-absolute-paths", "-keep-object-files", "-positions")
	}
	libzDir := moduleDir("modernc.org/libz")
	libzInclude := filepath.Join(libzDir, "include", goos, goarch)
	libsqlite3Dir := moduleDir("modernc.org/libsqlite3")
	libsqlite3Include := filepath.Join(libsqlite3Dir, "include")
	includeFlags := "-I" + libzInclude + " -I" + libsqlite3Include
	// Always parse external-library consumers against the matching modernc
	// headers. The native compiler also sees these flags while producing build
	// helpers; platform libz/libsqlite3 are ABI-compatible and satisfy
	// python.exe below.
	configureEnv = append(configureEnv,
		"CFLAGS="+strings.TrimSpace(os.Getenv("CFLAGS")+" "+includeFlags),
		"CPPFLAGS="+strings.TrimSpace(os.Getenv("CPPFLAGS")+" "+includeFlags),
		"ZLIB_CFLAGS=-I"+libzInclude,
		"ZLIB_LIBS=-lz",
		"LIBSQLITE3_CFLAGS=-I"+libsqlite3Include,
		"LIBSQLITE3_LIBS=-lsqlite3",
	)
	switch target {
	case "darwin/arm64", "darwin/amd64":
		gsed = "gsed"
		// ccgo resolves <limits.h> to clang's builtin header whose
		// #include_next never reaches the SDK copy, losing SSIZE_MAX,
		// PATH_MAX, IOV_MAX, ... Search the SDK before the builtin headers
		// but after the -I dirs (the SDK ships an expat.h too).
		sdk, err := exec.Command("xcrun", "--show-sdk-path").Output()
		if err != nil {
			fail(1, "xcrun --show-sdk-path: %v", err)
		}
		ccArgs = append(ccArgs,
			"-isystem", filepath.Join(strings.TrimSpace(string(sdk)), "usr", "include"),
			"-ignore-static-asserts",
			// modernc.org/libc (darwin) declares these with the wrong Go
			// signature; route them to our shims in libpython/libc_darwin.go.
			"-Dsched_yield=ccgo_sched_yield",
			"-Dwcschr=ccgo_wcschr",
		)
	case "windows/amd64", "windows/arm64":
		triple := env("MINGW_TRIPLE", "x86_64-w64-mingw32")
		compiler, err := exec.LookPath(triple + "-gcc")
		if err != nil {
			fail(1, "%s-gcc: %v", triple, err)
		}
		ar, err := exec.LookPath(triple + "-ar")
		if err != nil {
			fail(1, "%s-ar: %v", triple, err)
		}
		for _, name := range []string{"CC", "CCGO_CC", "CCGO_GCC", "CCGO_CLANG", "CCGO_CPP"} {
			os.Setenv(name, compiler)
		}
		// ccgo records the pre-shim `gcc`/`ar` from PATH as the real tools.
		// Put cross-tool wrappers first so make can use the shim-compatible
		// names without the native side accidentally falling back to host GCC.
		toolDir, _ := filepath.Abs(filepath.Join(tmp, goos+"_"+goarch, "tools"))
		mkdirAll(toolDir)
		for name, tool := range map[string]string{"gcc": compiler, "ar": ar} {
			wrapper := filepath.Join(toolDir, name)
			if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \""+tool+"\" \"$@\"\n"), 0o755); err != nil {
				fail(1, "%v", err)
			}
		}
		os.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		ccArgs = append(ccArgs,
			"--cpp", compiler,
			"--goos", goos,
			"--goarch", goarch,
			"-map", "gcc=gcc,ar=ar",
			// cc/v4 cannot parse LLVM 23's x86 intrinsic headers. CPython's
			// configured GCC-compatible paths do not need their inline bodies.
			"-D__INTRIN_H=1",
			"-D__X86INTRIN_H=1",
			// mingw-w64 provides ssize_t but not the POSIX SSIZE_MAX macro.
			"-DSSIZE_MAX=INTPTR_MAX",
			"-DPATH_MAX=260",
		)
	}
	configureEnv = append(configureEnv, configureEnvOS[goos]...)
	if goos == "darwin" && goarch != runtime.GOARCH {
		// Cross-generate the other darwin architecture: a compiler wrapper
		// adds -arch, used both by configure/make (the ccgo cc shim forwards
		// the flags to the host compiler; Rosetta runs the build helpers) and
		// by ccgo for the predefined macros (--cpp).
		arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch]
		mkdirAll(filepath.Dir(build))
		wrapper, _ := filepath.Abs(filepath.Join(filepath.Dir(build), "cc-"+arch))
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec cc -arch "+arch+" \"$@\"\n"), 0o755); err != nil {
			fail(1, "%v", err)
		}
		configureEnv = append(configureEnv, "CC="+wrapper)
		ccArgs = append(ccArgs, "--cpp="+wrapper)
	}

	// GO_GENERATE_INCREMENTAL=1 reuses tmp/cpython and tmp/build (make only
	// rebuilds what changed); GO_GENERATE_SKIP_BUILD=1 only relinks;
	// GO_GENERATE_POSTPROCESS=1 only re-runs the rewrites and the split on
	// the existing single file.
	mkdirAll(outDir)
	base := fmt.Sprintf("ccgo_%s_%s", goos, goarch)
	result := filepath.Join(outDir, base+".go")
	switch {
	case os.Getenv("GO_GENERATE_POSTPROCESS") != "":
		postprocess(result, base)
		return
	case os.Getenv("GO_GENERATE_SKIP_BUILD") != "":
	case os.Getenv("GO_GENERATE_INCREMENTAL") != "":
		if goos == "windows" {
			ccExec(append(ccArgs, "-exec", "make", "-C", build, "-j", j,
				"CC=gcc", "AR=ar", "GITVERSION=:", "GITTAG=:", "GITBRANCH=:", libName))
		} else {
			ccExec(append(ccArgs, "-exec", "make", "-C", build, "-j", j, libName))
		}
	default:
		prepareSource()
		removeAll(build)
		mkdirAll(build)
		configure(src)
		if goos == "windows" {
			ccExec(append(ccArgs, "-exec", "make", "-C", build, "-j", j,
				"CC=gcc", "AR=ar", "GITVERSION=:", "GITTAG=:", "GITBRANCH=:", libName))
		} else {
			ccExec(append(ccArgs, "-exec", "make", "-C", build, "-j", j, libName))
		}
	}

	hacl, _ := filepath.Glob(filepath.Join(build, "Modules", "_hacl", "*.a"))
	linkArgs := append(ccArgs,
		"--package-name", "libpython",
		"-o", result,
		filepath.Join(build, libName),
		filepath.Join(build, "Modules", "_decimal", "libmpdec", "libmpdec.a"),
		filepath.Join(build, "Modules", "expat", "libexpat.a"),
	)
	linkArgs = append(linkArgs, hacl...)
	// Keep libz after the archives: ccgo's package-backed library extraction,
	// like a traditional static linker, is order-sensitive.
	linkArgs = append(linkArgs, "-Lmodernc.org", "-lz", "-lsqlite3")
	ccMain(linkArgs)
	postprocess(result, base)
	if goos != "windows" {
		sysconfigdata()
	}
}

// moduleDir returns the local module-cache directory for a dependency. Using
// go list keeps generation independent of GOPATH and of the cache mount used
// by the platform builder containers.
func moduleDir(module string) string {
	list := func() string {
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
		out, err := cmd.Output()
		if err != nil {
			fail(1, "locate %s: %v", module, err)
		}
		return strings.TrimSpace(string(out))
	}
	dir := list()
	if dir == "" {
		// Fresh builder volumes know the module from go.mod, but go list does
		// not populate Dir until the module itself has been downloaded.
		cmd := exec.Command("go", "mod", "download", module)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fail(1, "download %s: %v", module, err)
		}
		dir = list()
	}
	if dir == "" {
		fail(1, "locate %s: empty module directory", module)
	}
	return dir
}

// sysconfigdata builds the native python.exe (the .o files are native) to
// export this platform's _sysconfigdata__<os>_<multiarch>.py into
// stdlib/sysconfigdata/, which mkstdlib packs into the embedded stdlib.
func sysconfigdata() {
	if goos == "darwin" && goarch != runtime.GOARCH {
		return // both darwin arches share _sysconfigdata__darwin_darwin.py; keep the native one
	}
	shell("make", "-C", build, "-j", j, "python.exe", "pybuilddir.txt")
	files, _ := filepath.Glob(filepath.Join(build, "build", "lib.*", "_sysconfigdata__*.py"))
	if len(files) != 1 {
		fail(1, "sysconfigdata: found %v", files)
	}
	mkdirAll(filepath.Join("stdlib", "sysconfigdata"))
	shell("cp", files[0], filepath.Join("stdlib", "sysconfigdata", filepath.Base(files[0])))
}

// postprocess applies the identifier and workaround rewrites to the single
// linked file, then shards it (internal/cmd/splitgo) into
// <base>_NN.go + <base>_data.go + <base>_data.bin and removes the single file.
func postprocess(result, base string) {
	shell(gsed, "-i", `s/\<T__\([a-zA-Z0-9][a-zA-Z0-9_]\+\)/t__\1/g`, result)
	shell(gsed, "-i", `s/\<x_\([a-zA-Z0-9_][a-zA-Z0-9_]\+\)/X\1/g`, result)
	// ccgo emits `__ccgo_fp(Xf)(tls, ...)` for a call through a cast function
	// designator, `((destructor)PyObject_Free)(op)`; call directly.
	shell(gsed, "-i", `s/__ccgo_fp(\(X[a-zA-Z0-9_]\+\))(/\1(/g`, result)
	// `char c = ENUM_CONST` with a value > 127 (pickle opcodes) becomes a
	// constant conversion Go rejects; make it a runtime conversion.
	shell(gsed, "-i", `s/\<int8(\(E[a-zA-Z0-9_]\+\))/libc.Int8FromInt32(int32(\1))/g`, result)
	// Compiler builtins libc does not provide (__builtin_nextafter, ...)
	// and libc helpers missing in this libc version are routed to
	// libpython/ccgo_shims.go.
	shell(gsed, "-i", `s/iqlibc\.X__builtin_\([a-zA-Z0-9_]\+\)(/_ccgo_builtin_\1(/g`, result)
	shell(gsed, "-i", `s/libc\.Atomic\(Load\|Store\)PUint\(8\|16\)(/_ccgo_Atomic\1PUint\2(/g`, result)
	// splitgo repeats imports and ccgo's blank-identifier import guards in
	// every shard. Package-backed libraries are only called from the shards
	// containing their extension modules, so add guards for all other shards.
	ensureImportGuard(result, `"modernc.org/libz"`, "var _ = libz.Xcrc32")
	ensureImportGuard(result, `"modernc.org/libsqlite3"`, "var _ = libsqlite3.Xsqlite3_libversion_number")
	// `_Py_FREELIST_FREE(name, op, Py_TYPE(op)->tp_free)` passes a function
	// pointer field into a static inline function; ccgo emits a direct call
	// on the uintptr field. Call through the pointer instead.
	shell(gsed, "-i", `s/(\*TPyTypeObject)(unsafe\.Pointer(\(v[0-9]*\)))\.Ftp_free(tls, \(v[0-9]*\))/(*(*func(*libc.TLS, uintptr))(unsafe.Pointer(\&struct{ uintptr }{(*TPyTypeObject)(unsafe.Pointer(\1)).Ftp_free})))(tls, \2)/g`, result)
	// ccgo emits a bare `vN` expression statement for an unused local
	// (Objects/gcmodule.c _PyObject_GC_Resize); Go rejects it.
	shell(gsed, "-i", `s/^\(\s*\)\(v[0-9]\+\)$/\1_ = \2/`, result)
	// libc functions whose darwin implementation is incomplete (panics with
	// TODO for arguments CPython uses) are routed to libpython/libc_darwin.go.
	for _, nm := range shimmedLibc[goos] {
		shell(gsed, "-i", fmt.Sprintf(`s/libc\.X%s(/_ccgo_%s(/g`, nm, nm), result)
	}
	for _, nm := range shimmedVars[goos] {
		shell(gsed, "-i", fmt.Sprintf(`s/libc\.X%s\>/_ccgo_%s/g`, nm, nm), result)
	}
	// C _Thread_local variables become per-libc.TLS slots (libpython/tls.go).
	for _, v := range threadLocals {
		shell(gsed, "-i", fmt.Sprintf(`/^var %s /d`, v[0]), result)
		shell(gsed, "-i", fmt.Sprintf(`s/\<%s\>/(*_ccgo_tls_%s(tls))/g`, v[0], v[1]), result)
	}
	old, _ := filepath.Glob(filepath.Join(outDir, base+"_*"))
	for _, v := range old {
		removeAll(v)
	}
	shell("go", "run", "./internal/cmd/splitgo", "-o", outDir, "-base", base, "-shards", "12", result)
	removeAll(result)
}

func ensureImportGuard(path, importText, guard string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(1, "%v", err)
	}
	s := string(b)
	if !strings.Contains(s, importText) || strings.Contains(s, guard) {
		return
	}
	const marker = "var _ unsafe.Pointer\n"
	if !strings.Contains(s, marker) {
		fail(1, "cannot place import guard %q in %s", guard, path)
	}
	s = strings.Replace(s, marker, marker+"\n"+guard+"\n", 1)
	if err := os.WriteFile(path, []byte(s), 0o660); err != nil {
		fail(1, "%v", err)
	}
}

// threadLocals maps the generated name of each C _Thread_local variable to
// its slot in libpython.tlsVars.
var threadLocals = [][2]string{
	{"X_Py_tss_tstate", "tstate"},
	{"Xpkgcontext", "pkgcontext"},
}

// shimmedVars lists, per GOOS, libc variables whose generated type does not
// match the declaration used by CPython. Keep each list sorted.
var shimmedVars = map[string][]string{
	"windows": {"in6addr_any"},
}

// shimmedLibc lists, per GOOS, the libc.X<name> calls rewritten to
// _ccgo_<name> (defined in libpython/libc_<goos>.go). Keep sorted.
var shimmedLibc = map[string][]string{"windows": {
	"CloseHandle",
	"CreateFileMappingA",
	"CreateMutexW",
	"ExitProcess",
	"FreeLibrary",
	"GetOverlappedResult",
	"GetProcAddress",
	"GetShortPathNameW",
	"GetUserNameW",
	"LoadLibraryW",
	"MessageBeep",
	"OutputDebugStringW",
	"RaiseException",
	"RegCloseKey",
	"RegConnectRegistryW",
	"RegCreateKeyExW",
	"RegDeleteKeyW",
	"RegDeleteValueW",
	"RegEnumKeyExW",
	"RegEnumValueW",
	"RegOpenKeyExW",
	"RegQueryValueExW",
	"RegSetValueExW",
	"SetErrorMode",
	"SetHandleInformation",
	"WSAGetLastError",
	"WSASetLastError",
	"__builtin_clzl",
	"__builtin_snprintf",
	"__mingw_vsnprintf",
	"__p__wenviron",
	"_wgetenv",
	"_wopen",
	"_wputenv",
	"abort",
	"accept",
	"bind",
	"closesocket",
	"connect",
	"dup2",
	"fdopen",
	"fprintf",
	"getpeername",
	"getservbyname",
	"getsockname",
	"getsockopt",
	"inet_ntoa",
	"ioctlsocket",
	"listen",
	"mbstowcs",
	"printf",
	"raise",
	"recv",
	"recvfrom",
	"select",
	"send",
	"sendto",
	"setlocale",
	"setsockopt",
	"shutdown",
	"snprintf",
	"socket",
	"sprintf",
	"sscanf",
	"strncat",
	"time",
	"umask",
	"ungetc",
	"vfprintf",
	"vsnprintf",
	"vsprintf",
	"wcschr",
	"wcsncmp",
	"wcstombs",
}, "darwin": {
	"__builtin___snprintf_chk",
	"__builtin___sprintf_chk",
	"__builtin___vsnprintf_chk",
	"__builtin_log2",
	"__maskrune",
	"__srget",
	"__tolower",
	"__toupper",
	"accept",
	"alarm",
	"cfgetospeed",
	"chflags",
	"chown",
	"confstr",
	"dup2",
	"endpwent",
	"fcntl",
	"fpathconf",
	"fprintf",
	"freeaddrinfo",
	"gai_strerror",
	"getaddrinfo",
	"getegid",
	"getgid",
	"getgrgid_r",
	"getgrnam_r",
	"getnameinfo",
	"getpwent",
	"getpwnam_r",
	"getpwuid_r",
	"getservbyname",
	"getsockopt",
	"inet_ntoa",
	"inet_ntop",
	"inet_pton",
	"kill",
	"link",
	"localeconv",
	"localtime",
	"localtime_r",
	"mbstowcs",
	"mknod",
	"mktime",
	"nanosleep",
	"nl_langinfo",
	"openpty",
	"pathconf",
	"poll",
	"pow",
	"printf",
	"pthread_key_delete",
	"pthread_sigmask",
	"raise",
	"read",
	"readv",
	"recv",
	"recvfrom",
	"recvmsg",
	"select",
	"send",
	"sendmsg",
	"sendto",
	"setenv",
	"setlocale",
	"setpwent",
	"setsockopt",
	"sigaction",
	"siginterrupt",
	"strerror",
	"strftime",
	"sysconf",
	"tcgetattr",
	"tcsetattr",
	"truncate",
	"ungetc",
	"unsetenv",
	"vfprintf",
	"write",
}, "linux": {
	// modernc's transpiled musl stdio locking hits an unsupported inline
	// asm barrier (atomic_arch.h) and aborts; the interpreter serializes
	// stdio itself.
	"clock_nanosleep",
	"flockfile",
	"funlockfile",
	"getitimer",
	"isalnum",
	"kill",
	"pause",
	"pthread_sigmask",
	"raise",
	"read",
	"recv",
	"recvfrom",
	"recvmsg",
	"send",
	"sendmsg",
	"sendto",
	"setenv",
	"setlocale",
	"setitimer",
	"sigaction",
	"siginterrupt",
	"sigpending",
	"sigtimedwait",
	"sigwait",
	"sigwaitinfo",
	"strcoll",
	"strxfrm",
	"syscall",
	"tolower",
	"toupper",
	"unsetenv",
	"wcscoll",
	"wcsxfrm",
	"write",
}}

// prepareSource copies $CPYTHON_SRC (a CPython 3.14 checkout) to tmp/cpython
// and applies internal/patch/*.diff there, leaving the original untouched.
func prepareSource() {
	orig := os.Getenv("CPYTHON_SRC")
	if orig == "" {
		fail(1, "CPYTHON_SRC must point to a CPython %s source tree", pyVer)
	}
	if _, err := os.Stat(filepath.Join(orig, "configure")); err != nil {
		fail(1, "CPYTHON_SRC=%s: %v", orig, err)
	}

	removeAll(srcCopy)
	mkdirAll(filepath.Dir(srcCopy))
	shell("cp", "-R", orig, srcCopy)
	patches, _ := filepath.Glob(filepath.Join("internal", "patch", "*.diff"))
	if goos == "windows" {
		// Match the proven plain cross-build order: the common ccgo patch is
		// applied before the pinned MSYS2 MinGW port.
		patches = append(patches, filepath.Join("internal", "patch", "windows", "cpython-3.14.7-msys2.diff"))
	}
	for _, p := range patches {
		b, err := os.ReadFile(p)
		if err != nil {
			fail(1, "%v", err)
		}

		cmd := exec.Command("patch", "-p1", "-d", srcCopy)
		cmd.Stdin = strings.NewReader(string(b))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fail(1, "patch %s: %v", p, err)
		}
	}
	if goos == "windows" {
		cmd := exec.Command("autoreconf", "-vfi")
		cmd.Dir = srcCopy
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fail(1, "autoreconf: %v", err)
		}
	}
}

func configure(src string) {
	args := configureArgs
	if goos == "windows" {
		triple := env("MINGW_TRIPLE", "x86_64-w64-mingw32")
		args = append(append([]string{}, configureArgs...),
			"--host="+triple,
			"--build="+env("BUILD_TRIPLE", "aarch64-unknown-linux-gnu"),
			"--prefix=/usr/local",
			"--with-build-python="+env("BUILD_PYTHON", "/usr/local/bin/python3.14"),
			"--disable-experimental-jit",
		)
		os.Setenv("CONFIG_SITE", env("CONFIG_SITE", filepath.Join("/src", "internal", "builders", "windows", "config.site."+goarch)))
		os.Setenv("CXX", triple+"-clang++")
		os.Setenv("AR", triple+"-ar")
		os.Setenv("RANLIB", triple+"-ranlib")
		os.Setenv("READELF", triple+"-readelf")
		os.Setenv("STRIP", triple+"-strip")
		os.Setenv("WINDRES", triple+"-windres")
	}
	cmd := exec.Command(filepath.Join(src, "configure"), args...)
	cmd.Dir = build
	cmd.Env = append(os.Environ(), configureEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fail(1, "configure: %v", err)
	}
}

func shell(cmd string, args ...string) {
	if out, err := util.Shell(nil, cmd, args...); err != nil {
		fail(1, "err=%v out=%s", err, out)
	}
}

func ccMain(args []string) {
	if err := ccgo.NewTask(goos, goarch, append([]string{"ccgo"}, args...), os.Stdout, os.Stderr, nil).Main(); err != nil {
		fail(1, "%v", err)
	}
}

func ccExec(args []string) {
	if err := ccgo.NewTask(goos, goarch, append([]string{"ccgo"}, args...), os.Stdout, os.Stderr, nil).Exec(); err != nil {
		fail(1, "%v", err)
	}
}

func mkdirAll(path string) {
	if err := os.MkdirAll(path, 0770); err != nil {
		fail(1, "%v", err)
	}
}

func removeAll(path string) {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		fail(1, "%v", err)
	}
}
