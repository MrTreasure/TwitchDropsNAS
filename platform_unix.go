//go:build !windows

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockBook(path string) (func(), error)     { return lockFile(path, false) }
func lockInstance(path string) (func(), error) { return lockFile(path, true) }
func lockFile(path string, nonblocking bool) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Chmod(0600); e == nil {
		mode := unix.LOCK_EX
		if nonblocking {
			mode |= unix.LOCK_NB
		}
		e = unix.Flock(int(f.Fd()), mode)
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func replacePrivate(from, to string) error { return os.Rename(from, to) }
func privateUmask()                        { unix.Umask(0077) }
