# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Windows Job Object that owns a child process and every descendant it starts.

taskkill /T walks parent links, so it misses a descendant whose parent already
exited, and closing a pipe does not interrupt a blocked ReadFile. A job created
before the child runs holds the whole tree: terminating it ends every process
that still has the child's output pipes open.
"""

from __future__ import annotations

import ctypes
import ctypes.wintypes

CREATE_SUSPENDED = 0x00000004

_JOB_OBJECT_EXTENDED_LIMIT_INFORMATION = 9
_JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000
_PROCESS_TERMINATE = 0x0001
_PROCESS_SET_QUOTA = 0x0100
_PROCESS_SUSPEND_RESUME = 0x0800


class _IoCounters(ctypes.Structure):
    _fields_ = [
        ("ReadOperationCount", ctypes.c_ulonglong),
        ("WriteOperationCount", ctypes.c_ulonglong),
        ("OtherOperationCount", ctypes.c_ulonglong),
        ("ReadTransferCount", ctypes.c_ulonglong),
        ("WriteTransferCount", ctypes.c_ulonglong),
        ("OtherTransferCount", ctypes.c_ulonglong),
    ]


class _BasicLimitInformation(ctypes.Structure):
    _fields_ = [
        ("PerProcessUserTimeLimit", ctypes.c_int64),
        ("PerJobUserTimeLimit", ctypes.c_int64),
        ("LimitFlags", ctypes.c_uint32),
        ("MinimumWorkingSetSize", ctypes.c_size_t),
        ("MaximumWorkingSetSize", ctypes.c_size_t),
        ("ActiveProcessLimit", ctypes.c_uint32),
        ("Affinity", ctypes.c_size_t),
        ("PriorityClass", ctypes.c_uint32),
        ("SchedulingClass", ctypes.c_uint32),
    ]


class _ExtendedLimitInformation(ctypes.Structure):
    _fields_ = [
        ("BasicLimitInformation", _BasicLimitInformation),
        ("IoInfo", _IoCounters),
        ("ProcessMemoryLimit", ctypes.c_size_t),
        ("JobMemoryLimit", ctypes.c_size_t),
        ("PeakProcessMemoryUsed", ctypes.c_size_t),
        ("PeakJobMemoryUsed", ctypes.c_size_t),
    ]


def _kernel32() -> ctypes.WinDLL:
    """Load kernel32 with the prototypes this module calls."""
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.CreateJobObjectW.argtypes = (ctypes.c_void_p, ctypes.wintypes.LPCWSTR)
    kernel32.CreateJobObjectW.restype = ctypes.wintypes.HANDLE
    kernel32.SetInformationJobObject.argtypes = (
        ctypes.wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, ctypes.wintypes.DWORD,
    )
    kernel32.SetInformationJobObject.restype = ctypes.wintypes.BOOL
    kernel32.OpenProcess.argtypes = (ctypes.wintypes.DWORD, ctypes.wintypes.BOOL, ctypes.wintypes.DWORD)
    kernel32.OpenProcess.restype = ctypes.wintypes.HANDLE
    kernel32.AssignProcessToJobObject.argtypes = (ctypes.wintypes.HANDLE, ctypes.wintypes.HANDLE)
    kernel32.AssignProcessToJobObject.restype = ctypes.wintypes.BOOL
    kernel32.TerminateJobObject.argtypes = (ctypes.wintypes.HANDLE, ctypes.wintypes.UINT)
    kernel32.TerminateJobObject.restype = ctypes.wintypes.BOOL
    kernel32.CloseHandle.argtypes = (ctypes.wintypes.HANDLE,)
    kernel32.CloseHandle.restype = ctypes.wintypes.BOOL
    return kernel32


def _last_error(operation: str) -> OSError:
    """Wrap the calling thread's last Windows error for one failed call."""
    code = ctypes.get_last_error()
    return OSError(code, f"{operation} failed with Windows error {code}")


class ProcessJob:
    """A kill-on-close job that holds one suspended child and its descendants."""

    def __init__(self, pid: int) -> None:
        self._kernel32 = _kernel32()
        self._job = self._create_job()
        try:
            self._adopt_and_resume(pid)
        except OSError:
            self.close()
            raise

    def _create_job(self) -> int:
        job = self._kernel32.CreateJobObjectW(None, None)
        if not job:
            raise _last_error("CreateJobObjectW")
        info = _ExtendedLimitInformation()
        info.BasicLimitInformation.LimitFlags = _JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not self._kernel32.SetInformationJobObject(
            job, _JOB_OBJECT_EXTENDED_LIMIT_INFORMATION, ctypes.byref(info), ctypes.sizeof(info),
        ):
            error = _last_error("SetInformationJobObject")
            self._kernel32.CloseHandle(job)
            raise error
        return job

    def _adopt_and_resume(self, pid: int) -> None:
        rights = _PROCESS_TERMINATE | _PROCESS_SET_QUOTA | _PROCESS_SUSPEND_RESUME
        process = self._kernel32.OpenProcess(rights, False, pid)
        if not process:
            raise _last_error("OpenProcess")
        try:
            if not self._kernel32.AssignProcessToJobObject(self._job, process):
                raise _last_error("AssignProcessToJobObject")
            ntdll = ctypes.WinDLL("ntdll")
            ntdll.NtResumeProcess.argtypes = (ctypes.wintypes.HANDLE,)
            ntdll.NtResumeProcess.restype = ctypes.c_long
            status = ntdll.NtResumeProcess(process)
            if status != 0:
                raise OSError(status, f"NtResumeProcess failed with NTSTATUS {status:#x}")
        finally:
            self._kernel32.CloseHandle(process)

    def terminate(self) -> None:
        """End every process in the job, which closes their inherited pipe handles."""
        if self._job and not self._kernel32.TerminateJobObject(self._job, 1):
            raise _last_error("TerminateJobObject")

    def close(self) -> None:
        """Release the job; kill-on-close ends any process still inside it."""
        if self._job:
            self._kernel32.CloseHandle(self._job)
            self._job = 0
