//go:build windows

package filelock

import (
	"errors"
	"syscall"
	"time"
)

// errorSharingViolation é ERROR_SHARING_VIOLATION (32), que o syscall não define.
const errorSharingViolation syscall.Errno = 32

func acquire(path string, timeout time.Duration) (func(), error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for {
		handle, err := syscall.CreateFile(name,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			0, // sem compartilhamento: é a trava
			nil,
			syscall.OPEN_ALWAYS,
			syscall.FILE_ATTRIBUTE_NORMAL,
			0)
		if err == nil {
			return func() { syscall.CloseHandle(handle) }, nil
		}
		if !errors.Is(err, errorSharingViolation) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(retryDelay)
	}
}
