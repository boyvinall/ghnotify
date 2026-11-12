package tray

import (
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
)

// openTarget opens a URL or file with the platform's default handler.
func openTarget(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		if strings.Contains(target, "://") {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
		} else {
			cmd = exec.Command("cmd", "/c", "start", "", target)
		}
		hideConsole(cmd)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		slog.Warn("open failed", "target", target, "err", err)
	}
}

// copyToClipboard writes text to the system clipboard.
func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
		hideConsole(cmd)
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		slog.Warn("copy to clipboard failed", "err", err)
	}
}
