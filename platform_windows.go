//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockBook(path string) (func(), error)     { return lockFile(path, false) }
func lockInstance(path string) (func(), error) { return lockFile(path, true) }
func lockFile(path string, nonblocking bool) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonblocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if e = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &overlapped); e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped); f.Close() }, nil
}
func replacePrivate(from, to string) error {
	f, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	t, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func privateUmask() {}
