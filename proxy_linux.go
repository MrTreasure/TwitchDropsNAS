//go:build linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
)

func memoryProxyCommand(binary, dir string, data []byte) (*exec.Cmd, error) {
	fd, e := unix.MemfdCreate("twitch-drops-proxy", unix.MFD_CLOEXEC)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "proxy-memory")
	if _, e = f.Write(data); e == nil {
		_, e = f.Seek(0, 0)
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	cmd := exec.Command(binary, "-d", dir, "-f", "/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	return cmd, nil
}
func closeProxyFiles(cmd *exec.Cmd) {
	for _, f := range cmd.ExtraFiles {
		f.Close()
	}
}
