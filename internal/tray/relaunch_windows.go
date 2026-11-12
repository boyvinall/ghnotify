//go:build windows

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// relaunch starts a helper that launches a new copy of ghnotify once this
// process has exited. The caller must quit after it returns nil.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	// Wait up to 10s for this process to exit and release its tray icon; if it
	// is still alive, give up rather than start a second instance.
	script := `$p = Get-Process -Id ` + strconv.Itoa(os.Getpid()) + ` -ErrorAction SilentlyContinue; ` +
		`if ($p -and -not $p.WaitForExit(10000)) { exit 1 }; ` +
		`Start-Process -FilePath '` + strings.ReplaceAll(exe, "'", "''") + `'`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	return nil
}
