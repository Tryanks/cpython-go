# Windows amd64 type-check gaps

Generated from:

```sh
go build -o tmp/typecheck ./internal/cmd/typecheck
GOOS=windows GOARCH=amd64 ./tmp/typecheck ./libpython
```

The checker reports **752 diagnostics**. It places the same type-check error in
both `packages.Package.Errors` and `packages.Package.TypeErrors`, so every
diagnostic is printed twice. After exact-line deduplication there are **376
source errors**: **368 undefined references** to **211 distinct symbols**, and
**8 non-undefined type errors**.

## Undefined symbols

Counts below are distinct symbols followed by deduplicated source references.

### CRT — 59 symbols, 93 references

```text
___daylight ___doserrno ___sys_errlist ___sys_nerr ___timezone
__aligned_free __chsize_s __cwait __get_osfhandle __getch __getche
__getwch __getwche __heapmin __kbhit __locking __open_osfhandle __putch
__putwch __set_errno __ungetch __ungetwch __wcsdup __wexecv __wexecve
__wfopen __wgetcwd __wputenv_s __wspawnv __wspawnve __wsystem
_clearerr _clock _dup _erf _erfc _exp2 _feof _gmtime_s _iswctype
_localeconv _localtime_s _mbrtowc _signal _strncat _strnicmp _swprintf
_towupper _wcscat_s _wcscoll _wcscpy_s _wcsftime _wcsncpy_s _wcsnlen
_wcsstr _wcstok_s _wcstol _wcsxfrm _wmemchr
```

### Win32 DLL imports — 145 symbols, 260 references

#### kernel32.dll — 94 symbols, 168 references

```text
_AcquireSRWLockExclusive _AddDllDirectory _AddVectoredExceptionHandler
_Beep _CancelIoEx _CompareStringOrdinal _ConnectNamedPipe _CopyFile2
_CreateIoCompletionPort _CreateNamedPipeW _CreateSemaphoreA
_CreateSymbolicLinkW _CreateToolhelp32Snapshot _CreateWaitableTimerExW
_DeleteProcThreadAttributeList _ExpandEnvironmentStringsW _FindFirstVolumeW
_FindNextVolumeW _FindVolumeClose _GenerateConsoleCtrlEvent
_GetActiveProcessorCount _GetConsoleOutputCP _GetCurrentThreadStackLimits
_GetDiskFreeSpaceExW _GetDriveTypeW _GetErrorMode
_GetFileInformationByHandleEx _GetFinalPathNameByHandleW
_GetHandleInformation _GetLocaleInfoA _GetLogicalDriveStringsW
_GetLongPathNameW _GetNamedPipeHandleStateW _GetNumberOfConsoleInputEvents
_GetProcessTimes _GetQueuedCompletionStatus _GetStringTypeW
_GetSystemTimePreciseAsFileTime _GetThreadTimes _GetTickCount64
_GetTimeZoneInformation _GetVersion _GetVolumePathNameW
_GetVolumePathNamesForVolumeNameW _InitializeConditionVariable
_InitializeProcThreadAttributeList _InitializeSRWLock _LCMapStringEx
_Module32FirstW _Module32NextW _MoveFileExW _NeedCurrentDirectoryForExePathW
_OpenEventW _OpenFileMappingW _OpenMutexW _OpenProcess _OpenThread
_PostQueuedCompletionStatus _PssCaptureSnapshot _PssFreeSnapshot
_PssQuerySnapshot _ReadProcessMemory _RegisterWaitForSingleObject
_ReleaseMutex _ReleaseSRWLockExclusive _ReleaseSemaphore _RemoveDllDirectory
_RemoveVectoredExceptionHandler _ResumeThread _RtlSecureZeroMemory
_SetEnvironmentVariableW _SetFileInformationByHandle _SetFilePointerEx
_SetLastError _SetNamedPipeHandleState _SetThreadStackGuarantee
_SetWaitableTimerEx _SleepConditionVariableSRW _SwitchToThread
_TerminateProcess _TlsAlloc _TlsFree _TlsGetValue _TlsSetValue
_UnregisterWait _UnregisterWaitEx _UpdateProcThreadAttribute _VirtualAlloc
_VirtualFree _VirtualQuery _WaitForMultipleObjects _WaitNamedPipeW
_WakeAllConditionVariable _WakeConditionVariable
```

#### ws2_32.dll — 25 symbols, 56 references

```text
_WSACleanup _WSAConnect _WSADuplicateSocketW _WSAIoctl _WSARecv
_WSARecvFrom _WSASend _WSASendTo _WSASetLastError _WSASocketW
_WSAStringToAddressW ___WSAFDIsSet _freeaddrinfo _getaddrinfo
_gethostbyaddr _gethostbyname _getnameinfo _getprotobyname _getservbyport
_inet_addr _inet_ntop _inet_pton _ntohl _recvfrom _sendto
```

#### advapi32.dll — 9 symbols, 11 references

```text
_AdjustTokenPrivileges _ConvertStringSecurityDescriptorToSecurityDescriptorW
_LookupPrivilegeValueA _RegCreateKeyW _RegDeleteKeyExW _RegFlushKey
_RegLoadKeyW _RegQueryInfoKeyW _RegSaveKeyW
```

#### version.dll — 5 symbols, 7 references

```text
_GetFileVersionInfoSizeW _GetFileVersionInfoW _VerQueryValueW
_VerSetConditionMask _VerifyVersionInfoA
```

#### iphlpapi.dll — 5 symbols, 7 references

```text
_ConvertInterfaceLuidToNameW _FreeMibTable _GetIfTable2Ex
_if_indextoname _if_nametoindex
```

#### rpcrt4.dll — 3 symbols, 6 references

```text
_RpcStringFreeW _UuidFromStringW _UuidToStringW
```

#### pathcch.dll — 2 symbols, 3 references

```text
_PathCchCombineEx _PathCchSkipRoot
```

#### bcrypt.dll — 1 symbol, 1 reference

```text
_BCryptGenRandom
```

#### winmm.dll — 1 symbol, 1 reference

```text
_PlaySoundW
```

### CPython static-inline helpers — 5 symbols, 13 references

These declarations survived preprocessing, but their C inline definitions did
not become linkable Go declarations.

```text
_PyLong_AsSocket_t _PyLong_FromSocket_t
__Py_atomic_load_llong_relaxed __Py_atomic_store_llong_relaxed
__Py_atomic_store_uint32_relaxed
```

### Other — 2 symbols, 2 references

```text
__BitScanReverse64
libc.Xstrncat
```

`__BitScanReverse64` is an MSVC-style compiler intrinsic. `libc.Xstrncat` is
referenced by the existing hand-written ccgo shim, but modernc.org/libc's
Windows surface does not define it.

## Non-undefined type errors

The eight deduplicated errors are ABI/type disagreements with the current
modernc.org/libc Windows declarations:

- Three `SetErrorMode` assignments expect `uint32`/`TUINT`, while
  `libc.XSetErrorMode` returns `int32`.
- Two `umask` errors pass an `int32` to `types.Mode_t` and assign the returned
  `types.Mode_t` (`uint16`) to `int32`.
- Two `GetShortPathNameW` assignments expect `TDWORD` (`uint32`), while
  `libc.XGetShortPathNameW` returns `int32`.
- One IPv6 initializer assigns libc's `[16]byte` `Xin6addr_any` to ccgo's
  generated `Tin6_addr`.

These are intentionally left for the Windows libc supplement/ABI milestone;
the current milestone does not attempt runtime support.
