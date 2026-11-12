package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func appDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ghnotify"), nil
	case "windows":
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("config dir: %w", err)
		}
		return filepath.Join(dir, "ghnotify"), nil
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "ghnotify"), nil
		}
		return filepath.Join(home, ".config", "ghnotify"), nil
	}
}

func dataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ghnotify"), nil
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "ghnotify"), nil
		}
		return filepath.Join(home, "AppData", "Local", "ghnotify"), nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "ghnotify"), nil
		}
		return filepath.Join(home, ".local", "share", "ghnotify"), nil
	}
}

// GetFilePath returns the platform-appropriate path for config.toml.
func GetFilePath() (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// StateFilePath returns the platform-appropriate path for state.json.
func StateFilePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// SnoozeFilePath returns the platform-appropriate path for snooze.json.
func SnoozeFilePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snooze.json"), nil
}

// AcknowledgeFilePath returns the platform-appropriate path for acknowledge.json.
func AcknowledgeFilePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "acknowledge.json"), nil
}
