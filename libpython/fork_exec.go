// Copyright 2026 The cpython-go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || linux

package libpython

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
)

var forkExecUmaskMu sync.Mutex

func pyIsNone(obj uintptr) bool {
	return obj == uintptr(unsafe.Pointer(&X_Py_NoneStruct))
}

func pyIsTypeOrSubtype(tls *libc.TLS, obj uintptr, typ uintptr) bool {
	if obj == 0 {
		return false
	}
	objType := (*TPyObject)(unsafe.Pointer(obj)).Fob_type
	return objType == typ || XPyType_IsSubtype(tls, objType, typ) != 0
}

func pyStringBytes(tls *libc.TLS, obj uintptr) (string, bool) {
	sizep := tls.Alloc(8)
	defer tls.Free(8)
	var data uintptr
	var size int64
	if pyIsTypeOrSubtype(tls, obj, uintptr(unsafe.Pointer(&XPyBytes_Type))) {
		data = XPyBytes_AsString(tls, obj)
		size = XPyBytes_Size(tls, obj)
	} else {
		data = XPyUnicode_AsUTF8AndSize(tls, obj, sizep)
		size = *(*int64)(unsafe.Pointer(sizep))
	}
	if data == 0 || size < 0 {
		return "", false
	}
	return string(cBytes(data, uint64(size))), true
}

func pyStringSequence(tls *libc.TLS, seq uintptr) ([]string, bool) {
	n := XPySequence_Size(tls, seq)
	if n < 0 {
		return nil, false
	}
	result := make([]string, 0, int(n))
	for i := int64(0); i < n; i++ {
		item := XPySequence_GetItem(tls, seq, i)
		if item == 0 {
			return nil, false
		}
		s, ok := pyStringBytes(tls, item)
		XPy_DecRef(tls, item)
		if !ok {
			return nil, false
		}
		result = append(result, s)
	}
	return result, true
}

func pyIntSequence(tls *libc.TLS, seq uintptr) ([]int, bool) {
	n := XPySequence_Size(tls, seq)
	if n < 0 {
		return nil, false
	}
	result := make([]int, 0, int(n))
	for i := int64(0); i < n; i++ {
		item := XPySequence_GetItem(tls, seq, i)
		if item == 0 {
			return nil, false
		}
		value := XPyLong_AsLong(tls, item)
		XPy_DecRef(tls, item)
		if value == -1 && XPyErr_Occurred(tls) != 0 {
			return nil, false
		}
		result = append(result, int(value))
	}
	return result, true
}

func setPythonErrorString(tls *libc.TLS, exception uintptr, message string) {
	p, err := libc.CString(message)
	if err != nil {
		setErrno(tls, int32(syscall.ENOMEM))
		XPyErr_SetFromErrno(tls, XPyExc_MemoryError)
		return
	}
	defer libc.Xfree(nil, p)
	XPyErr_SetString(tls, exception, p)
}

func childFiles(closeFds bool, keep []int, errpipeWrite int, p2cread, c2pwrite, errwrite int32) []uintptr {
	maxfd := 2
	for _, fd := range keep {
		if fd > maxfd && fd != errpipeWrite {
			maxfd = fd
		}
	}
	if !closeFds {
		maxfd = 1023
	}
	files := make([]uintptr, maxfd+1)
	closed := ^uintptr(0)
	for i := range files {
		files[i] = closed
	}
	files[0], files[1], files[2] = 0, 1, 2
	if p2cread >= 0 {
		files[0] = uintptr(p2cread)
	}
	if c2pwrite >= 0 {
		files[1] = uintptr(c2pwrite)
	}
	if errwrite >= 0 {
		files[2] = uintptr(errwrite)
	}
	for _, fd := range keep {
		// The C implementation keeps errpipe_write only until exec and relies
		// on FD_CLOEXEC. ProcAttr.Files clears CLOEXEC on every listed fd, so
		// omitting it is the only way to preserve the required EOF-on-exec.
		if fd >= 0 && fd < len(files) && fd != errpipeWrite {
			files[fd] = uintptr(fd)
		}
	}
	if !closeFds {
		// ponytail: CPython asks sysconf(_SC_OPEN_MAX); bound the scan at 1024
		// so a corrupt or enormous process limit cannot make Files unbounded.
		for fd := 3; fd < len(files); fd++ {
			if fd == errpipeWrite || files[fd] != closed {
				continue
			}
			flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			if err == nil && flags&unix.FD_CLOEXEC == 0 {
				files[fd] = uintptr(fd)
			}
		}
	}
	return files
}

func writeAllFD(fd int, data []byte) {
	for len(data) != 0 {
		n, err := unix.Write(fd, data)
		if err != nil || n == 0 {
			return
		}
		data = data[n:]
	}
}

func syscallErrno(err error) syscall.Errno {
	var n syscall.Errno
	if errors.As(err, &n) {
		return n
	}
	return syscall.EIO
}

func deadChildPID() (int, error) {
	return syscall.ForkExec("/usr/bin/true", []string{"true"}, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
	})
}

func _ccgo_fork_exec(tls *libc.TLS, processArgs, executableList uintptr, closeFds int32,
	pyFdsToKeep, cwdObj, envList uintptr,
	p2cread, p2cwrite, c2pread, c2pwrite, errread, errwrite, errpipeRead, errpipeWrite int32,
	restoreSignals, callSetsid, pgidToSet int32,
	gidObject, extraGroupsPacked, uidObject uintptr, childUmask int32, preexecFn uintptr) uintptr {
	_ = p2cwrite
	_ = c2pread
	_ = errread
	_ = errpipeRead
	_ = restoreSignals // Go's fork/exec path restores child signal state.

	if !pyIsNone(preexecFn) {
		setPythonErrorString(tls, XPyExc_RuntimeError, "preexec_fn is not supported")
		return 0
	}

	argv, ok := pyStringSequence(tls, processArgs)
	if !ok {
		return 0
	}
	executables, ok := pyStringSequence(tls, executableList)
	if !ok {
		return 0
	}
	keep, ok := pyIntSequence(tls, pyFdsToKeep)
	if !ok {
		return 0
	}

	env := os.Environ()
	if !pyIsNone(envList) {
		env, ok = pyStringSequence(tls, envList)
		if !ok {
			return 0
		}
	}
	cwd := ""
	if !pyIsNone(cwdObj) {
		cwd, ok = pyStringBytes(tls, cwdObj)
		if !ok {
			return 0
		}
	}

	sys := &syscall.SysProcAttr{Setsid: callSetsid != 0}
	if pgidToSet >= 0 {
		sys.Setpgid = true
		sys.Pgid = int(pgidToSet)
	}
	if !pyIsNone(uidObject) || !pyIsNone(gidObject) {
		cred := &syscall.Credential{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
		if !pyIsNone(uidObject) {
			uid := XPyLong_AsUnsignedLong(tls, uidObject)
			if uid == ^uint64(0) && XPyErr_Occurred(tls) != 0 {
				return 0
			}
			cred.Uid = uint32(uid)
		}
		if !pyIsNone(gidObject) {
			gid := XPyLong_AsUnsignedLong(tls, gidObject)
			if gid == ^uint64(0) && XPyErr_Occurred(tls) != 0 {
				return 0
			}
			cred.Gid = uint32(gid)
		}
		if !pyIsNone(extraGroupsPacked) {
			groups, groupsOK := pyIntSequence(tls, extraGroupsPacked)
			if !groupsOK {
				return 0
			}
			cred.Groups = make([]uint32, len(groups))
			for i, group := range groups {
				cred.Groups[i] = uint32(group)
			}
		}
		sys.Credential = cred
	}

	attr := &syscall.ProcAttr{
		Dir:   cwd,
		Env:   env,
		Files: childFiles(closeFds != 0, keep, int(errpipeWrite), p2cread, c2pwrite, errwrite),
		Sys:   sys,
	}

	var lastErr error = syscall.ENOENT
	chdirFailure := false
	if cwd != "" {
		if _, err := os.Stat(cwd); err != nil {
			lastErr = err
			chdirFailure = true
			executables = nil
		}
	}
	for _, executable := range executables {
		var pid int
		var err error
		if childUmask >= 0 {
			// ponytail: umask is process-global. The interpreter currently
			// serializes Python execution; this mutex serializes our spawn calls.
			forkExecUmaskMu.Lock()
			old := unix.Umask(int(childUmask))
			pid, err = syscall.ForkExec(executable, argv, attr)
			unix.Umask(old)
			forkExecUmaskMu.Unlock()
		} else {
			pid, err = syscall.ForkExec(executable, argv, attr)
		}
		if err == nil {
			return XPyLong_FromLong(tls, int64(pid))
		}
		lastErr = err
		n := syscallErrno(err)
		if n != syscall.ENOENT && n != syscall.ENOTDIR && n != syscall.EACCES {
			break
		}
	}

	errnoValue := syscallErrno(lastErr)
	suffix := ""
	if chdirFailure {
		suffix = "noexec:chdir"
	}
	writeAllFD(int(errpipeWrite), []byte(fmt.Sprintf("OSError:%x:%s", uint32(errnoValue), suffix)))

	// ForkExec reaps its failed child before returning, while subprocess.py
	// expects a PID it can wait for after consuming errpipe_data. Supply a
	// harmless short-lived child so that waitpid retains the normal contract.
	pid, err := deadChildPID()
	if err != nil {
		setErrno(tls, int32(syscallErrno(err)))
		XPyErr_SetFromErrno(tls, XPyExc_OSError)
		return 0
	}
	return XPyLong_FromLong(tls, int64(pid))
}
