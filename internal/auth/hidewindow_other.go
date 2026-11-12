//go:build !windows

package auth

import "os/exec"

func hideWindow(*exec.Cmd) {}
