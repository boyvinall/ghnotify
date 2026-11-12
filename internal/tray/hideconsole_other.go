//go:build !windows

package tray

import "os/exec"

func hideConsole(*exec.Cmd) {}
