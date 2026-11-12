//go:build !windows

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// relaunch starts a helper that launches a new copy of ghnotify once this
// process has exited. The caller must quit after it returns nil.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	// Inside a bundle, relaunch the .app so macOS treats it as the same app.
	target := exe
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		target = exe[:i+len(".app")]
	}
	launch := `"$2" &`
	if target != exe {
		launch = `open -n "$2"`
	}
	// Wait up to 10s for this process to exit and release its tray icon; if it
	// is still alive, give up rather than start a second instance. The target
	// is a positional argument so the shell never parses the path.
	script := `i=0; while kill -0 "$1" 2>/dev/null; do [ $i -ge 100 ] && exit 1; sleep 0.1; i=$((i+1)); done; ` + launch
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), target)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	return nil
}
