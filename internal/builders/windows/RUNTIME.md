# Windows runtime readiness

This is the first-run checklist for the hand-written Windows amd64 supplement.
“Real” means the wrapper has a concrete implementation or forwards to the
documented UCRT/Win32 API. “Partial” records a known semantic ceiling. “Stub”
records the deterministic failure behavior. Win32 failures are mirrored into
both `libc.TLS.SetLastError` and the errno slot read by modernc's
`libc.XGetLastError`; APIs that return HRESULT, NTSTATUS, RPC_STATUS, LSTATUS,
or Winsock error codes return those values directly.

No Windows runtime execution was available while this file was written. Check
each area on `windows-latest`, with special attention to the partial entries and
to native callback/function-pointer boundaries.

## UCRT and pure-Go compatibility

- `___daylight` — real: returns UCRT's `_daylight` storage through `__daylight`.
- `___doserrno` — real: returns UCRT's per-thread DOS-error storage through `__doserrno`.
- `___sys_errlist` — real: returns UCRT's error-string table through `__sys_errlist`.
- `___sys_nerr` — real: returns UCRT's error-table bound through `__sys_nerr`.
- `___timezone` — real: returns UCRT's `_timezone` storage through `__timezone`.
- `__aligned_free` — real: forwards to UCRT `_aligned_free` so aligned allocations use the matching allocator.
- `__chsize_s` — real: resizes the modernc descriptor's Windows handle and restores its file position; returns an errno value on failure.
- `__cwait` — partial: waits only synthetic handles returned by these `__wspawnv[e]` wrappers; `action` is ignored and unknown handles fail with `ECHILD`.
- `__get_osfhandle` — real: resolves modernc's private descriptor table to its native `HANDLE`; invalid descriptors return `-1`/`EBADF`.
- `__getch` — real: forwards to UCRT `_getch`.
- `__getche` — real: forwards to UCRT `_getche`.
- `__getwch` — real: forwards to UCRT `_getwch`.
- `__getwche` — real: forwards to UCRT `_getwche`.
- `__heapmin` — real no-op: returns success, as allowed for the Go heap.
- `__kbhit` — real: forwards to UCRT `_kbhit`.
- `__locking` — partial: locks the region at the current modernc descriptor offset; nonblocking modes are honored, while UCRT's ten one-second retries for blocking modes are replaced by a blocking Win32 lock.
- `__open_osfhandle` — partial: inserts the native handle into modernc's private descriptor table and transfers ownership; text/binary and append flags are not retained.
- `__putch` — real: forwards to UCRT `_putch`.
- `__putwch` — real: forwards to UCRT `_putwch`.
- `__set_errno` — real: writes the modernc per-TLS errno slot and returns zero.
- `__ungetch` — real: forwards to UCRT `_ungetch`.
- `__ungetwch` — real: forwards to UCRT `_ungetwch`.
- `__wcsdup` — real: duplicates UTF-16 storage with `libc.Xmalloc`; allocation failure reports `ENOMEM`.
- `__wexecv` — partial by platform: spawns, waits, and exits with the child status because Go cannot replace the current process image; launch failure returns `-1`.
- `__wexecve` — partial by platform: same spawn/wait/exit ceiling as `__wexecv`, with the supplied environment.
- `__wfopen` — real: converts UTF-16 to a Go/UTF-8 path and uses `libc.Xfopen`, preserving modernc's `FILE` and descriptor tables.
- `__wgetcwd` — real: writes or allocates a NUL-terminated UTF-16 working directory; short buffers report `ERANGE`.
- `__wputenv_s` — partial: validates the UCRT contract and updates Go's process environment, which interoperates with modernc; it does not update a separately cached UCRT `_wenviron` array.
- `__wspawnv` — partial: implements `_P_WAIT`, `_P_NOWAIT`, `_P_NOWAITO`, `_P_OVERLAY`, and `_P_DETACH` with `os/exec`; asynchronous results are synthetic handles understood by `__cwait`.
- `__wspawnve` — partial: same mode/handle ceiling as `__wspawnv`, with the supplied environment.
- `__wsystem` — real for normal commands: runs `cmd.exe /c`, returns its exit status, and reports `ENOENT` on launcher failure.
- `_clearerr` — partial: clears modernc's stream error flag; modernc exposes no independent EOF flag to clear.
- `_clock` — real: forwards to UCRT `clock` and returns its millisecond `clock_t` value.
- `_dup` — real: duplicates the underlying Windows handle and registers the duplicate in modernc's descriptor table.
- `_erf` — real: implemented with `math.Erf`.
- `_erfc` — real: implemented with `math.Erfc`.
- `_exp2` — real: implemented with `math.Exp2`.
- `_feof` — stub: returns zero because modernc's Windows `FILE` representation does not retain an EOF indicator; it does not set errno.
- `_gmtime_s` — real: fills the generated UCRT `tm` layout from a 64-bit `time_t` in UTC; invalid pointers return `EINVAL`.
- `_iswctype` — real: forwards to UCRT `_iswctype`.
- `_localeconv` — real: returns UCRT's native `lconv` pointer.
- `_localtime_s` — real: fills the generated UCRT `tm` layout from a 64-bit `time_t` in the local zone, including hemisphere-independent DST detection; invalid pointers return `EINVAL`.
- `_mbrtowc` — real: forwards to UCRT `mbrtowc`, preserves its `mbstate_t`, and mirrors UCRT errno into modernc errno.
- `_signal` — partial: stores and returns ccgo handler values but cannot register them as native callbacks, so handlers are not delivered by the OS.
- `_strncat` — real: appends at most `n` bytes and always writes the required terminator for valid C inputs.
- `_strnicmp` — real: forwards to UCRT `_strnicmp`.
- `_swprintf` — partial: consumes the ccgo `va_list` through the shared C formatter and writes UTF-16; CPython's current `cp%u` call is covered, but the formatter does not implement every Microsoft wide-printf extension.
- `_towupper` — real: forwards to UCRT `towupper`.
- `_wcscat_s` — partial: implements bounds checks, buffer clearing, and `EINVAL`/`ERANGE`; it does not invoke UCRT's invalid-parameter handler.
- `_wcscoll` — real: forwards to UCRT `wcscoll` and therefore uses the active UCRT locale.
- `_wcscpy_s` — partial: implements bounds checks, buffer clearing, and `EINVAL`/`ERANGE`; it does not invoke UCRT's invalid-parameter handler.
- `_wcsftime` — partial: uses the shared English `strftime` formatter, local-zone offsets, common modifiers, and UTF-16 output; alternative-era and locale-specific names are not implemented.
- `_wcsncpy_s` — partial: implements the counts used by CPython plus bounds checks and `EINVAL`/`ERANGE`; `_TRUNCATE` and the invalid-parameter handler are not implemented.
- `_wcsnlen` — real: scans at most the supplied UTF-16 element count.
- `_wcsstr` — real: returns a pointer into the original UTF-16 haystack or zero.
- `_wcstok_s` — real for valid inputs: tokenizes in place and updates the caller's context pointer.
- `_wcstol` — partial: implements bases 0 and 2–36, ASCII whitespace/sign/prefix handling, end pointers, and 32-bit saturation; it does not use locale-specific digits.
- `_wcsxfrm` — real: forwards to UCRT `wcsxfrm` and returns the required wide-character count.
- `_wmemchr` — real: scans the requested UTF-16 element range and returns the matching address or zero.

## kernel32.dll

- `_AcquireSRWLockExclusive` — real: direct `AcquireSRWLockExclusive` call.
- `_AddDllDirectory` — real: direct call; failure returns zero and mirrors LastError.
- `_AddVectoredExceptionHandler` — stub: returns zero and reports `ERROR_CALL_NOT_IMPLEMENTED` (120), because a ccgo Go function value is not a native exception callback.
- `_Beep` — real: direct call with LastError propagation on failure.
- `_CancelIoEx` — real: uses `windows.CancelIoEx` and the caller's `OVERLAPPED` storage.
- `_CompareStringOrdinal` — real: direct call with the exact caller lengths and case flag.
- `_ConnectNamedPipe` — real: uses `windows.ConnectNamedPipe`; expected statuses such as `ERROR_PIPE_CONNECTED` are preserved in LastError.
- `_CopyFile2` — real for CPython's use: direct call returning HRESULT; the current caller supplies no progress callback.
- `_CreateIoCompletionPort` — real: direct call preserving completion keys and output handle.
- `_CreateNamedPipeW` — real: uses `windows.CreateNamedPipe` and returns `INVALID_HANDLE_VALUE` on failure.
- `_CreateSemaphoreA` — real: direct call returning a native semaphore handle.
- `_CreateSymbolicLinkW` — real: uses `windows.CreateSymbolicLink` and preserves its byte-sized BOOLEAN result.
- `_CreateToolhelp32Snapshot` — real: direct call returning `INVALID_HANDLE_VALUE` on failure.
- `_CreateWaitableTimerExW` — real: direct call returning a native timer handle.
- `_DeleteProcThreadAttributeList` — real: direct void call.
- `_ExpandEnvironmentStringsW` — real: direct call including required-size behavior.
- `_FindFirstVolumeW` — real: uses `windows.FindFirstVolume`.
- `_FindNextVolumeW` — real: uses `windows.FindNextVolume`.
- `_FindVolumeClose` — real: uses `windows.FindVolumeClose`.
- `_GenerateConsoleCtrlEvent` — real: direct call with LastError propagation.
- `_GetActiveProcessorCount` — real: direct processor-group query.
- `_GetConsoleOutputCP` — real: direct query.
- `_GetCurrentThreadStackLimits` — real: direct void call writing both caller outputs.
- `_GetDiskFreeSpaceExW` — real: uses `windows.GetDiskFreeSpaceEx` and writes all optional `uint64` outputs.
- `_GetDriveTypeW` — real: direct call preserving the zero failure result and LastError.
- `_GetErrorMode` — real: direct query.
- `_GetFileInformationByHandleEx` — real: uses `windows.GetFileInformationByHandleEx` with the generated information-class value.
- `_GetFinalPathNameByHandleW` — real: uses `windows.GetFinalPathNameByHandle`, including required-size returns.
- `_GetHandleInformation` — real: direct call writing the caller's flags.
- `_GetLocaleInfoA` — real: direct ANSI locale query.
- `_GetLogicalDriveStringsW` — real: uses `windows.GetLogicalDriveStrings`.
- `_GetLongPathNameW` — real: direct call including required-size behavior.
- `_GetNamedPipeHandleStateW` — real: uses `windows.GetNamedPipeHandleState` and preserves optional outputs.
- `_GetNumberOfConsoleInputEvents` — real: direct call writing the event count.
- `_GetProcessTimes` — real: uses `windows.GetProcessTimes` and writes the four `FILETIME` values.
- `_GetQueuedCompletionStatus` — real: direct call writing byte count, completion key, and `OVERLAPPED` pointer.
- `_GetStringTypeW` — real: direct call writing the caller's character-type array.
- `_GetSystemTimePreciseAsFileTime` — real: direct void call writing `FILETIME`.
- `_GetThreadTimes` — real: direct call writing the four `FILETIME` values.
- `_GetTickCount64` — real: direct monotonic tick query.
- `_GetTimeZoneInformation` — real: direct call returning the documented ID or `TIME_ZONE_ID_INVALID`.
- `_GetVersion` — real: direct legacy version query.
- `_GetVolumePathNameW` — real: direct call writing the path buffer.
- `_GetVolumePathNamesForVolumeNameW` — real: direct call preserving multistring and required-size outputs.
- `_InitializeConditionVariable` — real: direct void initialization.
- `_InitializeProcThreadAttributeList` — real: direct call including the sizing probe convention.
- `_InitializeSRWLock` — real: direct void initialization.
- `_LCMapStringEx` — real: direct call preserving all optional parameters and sort handle.
- `_Module32FirstW` — real: direct call writing the generated module-entry layout.
- `_Module32NextW` — real: direct call writing the generated module-entry layout.
- `_MoveFileExW` — real: uses `windows.MoveFileEx`.
- `_NeedCurrentDirectoryForExePathW` — real: direct query.
- `_OpenEventW` — real: direct call returning a native event handle.
- `_OpenFileMappingW` — real: direct call returning a native mapping handle.
- `_OpenMutexW` — real: direct call returning a native mutex handle.
- `_OpenProcess` — real: uses `windows.OpenProcess`.
- `_OpenThread` — real: direct call returning a native thread handle.
- `_PostQueuedCompletionStatus` — real: direct call preserving the pointer-sized completion key.
- `_PssCaptureSnapshot` — real: direct call returning its Win32 status code and writing the snapshot handle.
- `_PssFreeSnapshot` — real: direct call returning its Win32 status code.
- `_PssQuerySnapshot` — real: direct call returning its Win32 status code and writing caller storage.
- `_ReadProcessMemory` — real: direct call writing the optional byte count.
- `_RegisterWaitForSingleObject` — stub: returns zero and reports `ERROR_CALL_NOT_IMPLEMENTED` (120), because its ccgo callback cannot be passed to native code.
- `_ReleaseMutex` — real: direct call with LastError propagation.
- `_ReleaseSRWLockExclusive` — real: direct void call.
- `_ReleaseSemaphore` — real: direct call writing the optional previous count.
- `_RemoveDllDirectory` — real: direct call.
- `_RemoveVectoredExceptionHandler` — stub: returns zero and reports `ERROR_CALL_NOT_IMPLEMENTED` (120), paired with the unsupported add operation.
- `_ResumeThread` — real: direct call preserving the `0xffffffff` failure result.
- `_RtlSecureZeroMemory` — real: clears the exact caller-supplied byte range in Go and returns the original pointer.
- `_SetEnvironmentVariableW` — real: uses `windows.SetEnvironmentVariable`, including nil-value deletion.
- `_SetFileInformationByHandle` — real: uses `windows.SetFileInformationByHandle`.
- `_SetFilePointerEx` — real: forwards the generated `LARGE_INTEGER` bit pattern and writes the optional new position.
- `_SetLastError` — real: updates the native thread LastError plus both modernc TLS locations used by generated code.
- `_SetNamedPipeHandleState` — real: uses `windows.SetNamedPipeHandleState` with optional pointers.
- `_SetThreadStackGuarantee` — real: direct call writing the requested/previous size.
- `_SetWaitableTimerEx` — real for CPython's use: direct call; the current call site passes a null completion routine, while arbitrary ccgo callbacks would not be native-callable.
- `_SleepConditionVariableSRW` — real: direct call with timeout/flags and LastError propagation.
- `_SwitchToThread` — real: direct scheduler-yield call.
- `_TerminateProcess` — real: uses `windows.TerminateProcess`.
- `_TlsAlloc` — real: direct allocation preserving `TLS_OUT_OF_INDEXES`.
- `_TlsFree` — real: direct release.
- `_TlsGetValue` — real: direct query preserving a legitimate zero value and error status.
- `_TlsSetValue` — real: direct setter.
- `_UnregisterWait` — real for native wait handles: direct call; this supplement never creates a wait because callback registration is unsupported.
- `_UnregisterWaitEx` — real for native wait handles: direct call; same registration ceiling as `_UnregisterWait`.
- `_UpdateProcThreadAttribute` — real: direct call with pointer-sized attribute and sizes.
- `_VirtualAlloc` — real: uses `windows.VirtualAlloc`.
- `_VirtualFree` — real: uses `windows.VirtualFree`.
- `_VirtualQuery` — real: direct call returning the actual byte count rather than the supplied capacity.
- `_WaitForMultipleObjects` — real: uses `windows.WaitForMultipleObjects`; a zero count returns `WAIT_FAILED`/`ERROR_INVALID_PARAMETER`.
- `_WaitNamedPipeW` — real: direct call.
- `_WakeAllConditionVariable` — real: direct void call.
- `_WakeConditionVariable` — real: direct void call.

## ws2_32.dll

- `_WSACleanup` — real: uses `windows.WSACleanup` and mirrors `WSAGetLastError` on failure.
- `_WSAConnect` — real: direct call preserving all optional QOS buffers.
- `_WSADuplicateSocketW` — real: uses `windows.WSADuplicateSocket` and writes `WSAPROTOCOL_INFOW`.
- `_WSAIoctl` — real for direct operations and CPython's null-completion calls: uses `windows.WSAIoctl`; native extension-function pointers returned by `SIO_GET_EXTENSION_FUNCTION_POINTER` still cross a generated ccgo/native-call ABI boundary and need runtime validation.
- `_WSARecv` — real for CPython's null-completion calls: uses `windows.WSARecv`, including overlapped I/O.
- `_WSARecvFrom` — real for CPython's null-completion calls: uses `windows.WSARecvFrom`, including overlapped I/O and address outputs.
- `_WSASend` — real for CPython's null-completion calls: uses `windows.WSASend`, including overlapped I/O.
- `_WSASendTo` — real for CPython's null-completion calls: uses `windows.WSASendTo`, including overlapped I/O.
- `_WSASetLastError` — real: sets native Winsock error state and mirrors it into modernc TLS.
- `_WSASocketW` — real: uses `windows.WSASocket` and returns `INVALID_SOCKET` on failure.
- `_WSAStringToAddressW` — real: direct Unicode parser with caller-supplied output length.
- `___WSAFDIsSet` — real: checks the Win64 `fd_set` count and 64 socket slots in libc memory.
- `_freeaddrinfo` — real for lists produced by this supplement: frees canonical name, socket address, and each libc-allocated `addrinfo` node.
- `_getaddrinfo` — real: converts UTF-8 C inputs to `GetAddrInfoW`, then builds a MinGW ANSI-layout linked list wholly in libc memory.
- `_gethostbyaddr` — real: direct legacy Winsock lookup with native static result storage.
- `_gethostbyname` — real: direct legacy Winsock lookup with native static result storage.
- `_getnameinfo` — real: direct ANSI/UTF-8-compatible Winsock call writing caller buffers.
- `_getprotobyname` — real: direct legacy Winsock lookup.
- `_getservbyport` — real: direct legacy Winsock lookup.
- `_inet_addr` — real: direct IPv4 parser.
- `_inet_ntop` — real: direct ANSI formatter with Winsock error propagation.
- `_inet_pton` — real: direct ANSI parser with Winsock error propagation.
- `_ntohl` — real: reverses the 32-bit byte order on Windows amd64.
- `_recvfrom` — real: direct Winsock receive with address outputs and WSA error propagation.
- `_sendto` — real: direct Winsock send with WSA error propagation.

## advapi32.dll

- `_AdjustTokenPrivileges` — real: direct call; even a successful `ERROR_NOT_ALL_ASSIGNED` status is mirrored for generated `GetLastError` checks.
- `_ConvertStringSecurityDescriptorToSecurityDescriptorW` — real: direct call writing the allocated descriptor and optional size.
- `_LookupPrivilegeValueA` — real: direct ANSI privilege lookup.
- `_RegCreateKeyW` — real: direct call returning `LSTATUS` and writing the key handle.
- `_RegDeleteKeyExW` — real: direct call returning `LSTATUS`.
- `_RegFlushKey` — real: direct call returning `LSTATUS`.
- `_RegLoadKeyW` — real: direct call returning `LSTATUS`.
- `_RegQueryInfoKeyW` — real: direct call preserving every optional output pointer.
- `_RegSaveKeyW` — real: direct call returning `LSTATUS`.

## version.dll and version helpers

- `_GetFileVersionInfoSizeW` — real: direct wide call writing the original 32-bit handle output without a Go-size reinterpretation.
- `_GetFileVersionInfoW` — real: uses `windows.GetFileVersionInfo` and caller storage.
- `_VerQueryValueW` — real: uses `windows.VerQueryValue`, returning a pointer into the caller's version block.
- `_VerSetConditionMask` — real: direct kernel32 helper.
- `_VerifyVersionInfoA` — real: direct kernel32 ANSI check with LastError propagation.

## iphlpapi.dll

- `_ConvertInterfaceLuidToNameW` — real: direct call returning its Win32 status code.
- `_FreeMibTable` — real: direct void release.
- `_GetIfTable2Ex` — real: direct call returning its Win32 status code and writing the allocated table pointer.
- `_if_indextoname` — real: direct ANSI name conversion with Winsock error propagation.
- `_if_nametoindex` — real: direct ANSI index lookup with Winsock error propagation.

## rpcrt4.dll

- `_RpcStringFreeW` — real: direct call returning `RPC_STATUS` and clearing the caller's string pointer.
- `_UuidFromStringW` — real: direct call returning `RPC_STATUS`.
- `_UuidToStringW` — real: direct call returning `RPC_STATUS` and writing an RPC-allocated UTF-16 string.

## pathcch.dll, bcrypt.dll, and winmm.dll

- `_PathCchCombineEx` — real: direct call returning HRESULT and writing the UTF-16 output buffer.
- `_PathCchSkipRoot` — real: direct call returning HRESULT and writing a pointer into the input path.
- `_BCryptGenRandom` — real: direct call returning NTSTATUS.
- `_PlaySoundW` — real: direct call with LastError propagation on failure.

## CPython inlines, compiler helpers, and routed ABI fixes

- `_PyLong_AsSocket_t` — real: matches `socketmodule.h` by converting through signed `PyLong_AsLongLong` and casting to Win64 `SOCKET`.
- `_PyLong_FromSocket_t` — real: matches `socketmodule.h` by casting Win64 `SOCKET` through signed `long long` into `PyLong_FromLongLong`.
- `__Py_atomic_load_llong_relaxed` — real: atomic 64-bit load; Go's stronger sequential consistency satisfies relaxed ordering.
- `__Py_atomic_store_llong_relaxed` — real: atomic 64-bit store; Go's stronger sequential consistency satisfies relaxed ordering.
- `__Py_atomic_store_uint32_relaxed` — real: atomic 32-bit store; Go's stronger sequential consistency satisfies relaxed ordering.
- `__BitScanReverse64` — real: returns zero for an empty mask or writes `63 - bits.LeadingZeros64(mask)` and returns one.
- `_ccgo_strncat` — real: libc-memory implementation used by both routed `strncat` and the checked-builtin shim.
- `libc.Xstrncat` — real: generated Windows calls are routed to `_ccgo_strncat` because modernc does not export this symbol on Windows.
- `_ccgo_GetShortPathNameW` — real: exact `DWORD` ABI wrapper with LastError propagation.
- `_ccgo_SetErrorMode` — real: exact unsigned `UINT` ABI wrapper.
- `_ccgo_umask` — partial: maintains a process-wide Go-side mask with the correct 32-bit ABI; Windows ACL creation is unaffected by POSIX mode bits.
- `_ccgo_in6addr_any` — real: generated-layout, all-zero IPv6 wildcard value.
- `PyBoolFromBool` — real: isolates the platform-dependent `PyBool_FromLong` integer width for root-package conversion.

## Pre-existing modernc runtime blockers outside this inventory

Type-check success does not imply that every already-declared modernc Windows
function is implemented. In v1.75.7, core socket entry points including
`Xsocket`, `Xbind`, `Xconnect`, `Xlisten`, `Xaccept`, `Xrecv`, `Xsend`,
`Xshutdown`, `Xgetsockopt`, `Xsetsockopt`, `Xselect`, `Xioctlsocket`,
`Xclosesocket`, and `XWSAGetLastError` still panic with `TODO`; `Xsetlocale`
returns zero unconditionally. Those symbols were not undefined and therefore
were not routed in this milestone, but the first Windows smoke test should
expect them to gate networking and locale initialization independently of the
supplement above.
