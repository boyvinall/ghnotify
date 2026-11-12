//go:build windows

package auth

import (
	"os/exec"
	"syscall"
)

// hideWindow stops the GUI-subsystem app from flashing a console for each gh call.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
