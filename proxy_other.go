//go:build !linux

package main

import (
	"errors"
	"os/exec"
)

func memoryProxyCommand(binary, dir string, data []byte) (*exec.Cmd, error) {
	return nil, errors.New("embedded mihomo needs Linux; use an explicit HTTP proxy on this platform")
}
func closeProxyFiles(cmd *exec.Cmd) {}
