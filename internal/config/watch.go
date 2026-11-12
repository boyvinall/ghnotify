package config

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce is how long the file must be quiet before it is re-read.
// Editors often save in several steps (write temp file, rename, chmod).
const watchDebounce = 300 * time.Millisecond

// Watch calls onChange with a fresh ParseResult each time the content of the
// config file at path differs from the last content it saw, starting with
// loaded (nil if unknown). It watches the parent directory rather than the
// file, because editors that save atomically replace the file, which would
// silently end a watch on the file itself. Saves that leave the content
// unchanged, and moments where the file is briefly missing, are ignored.
//
// The watcher stops when ctx is cancelled.
func Watch(ctx context.Context, path string, loaded []byte, onChange func(ParseResult)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(filepath.Dir(path)); err != nil {
		_ = w.Close()
		return err
	}

	go func() {
		defer func() { _ = w.Close() }()
		last, known := loaded, loaded != nil
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		if known {
			timer.Reset(watchDebounce)
		}
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Clean(ev.Name) == filepath.Clean(path) {
					timer.Reset(watchDebounce)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				slog.Warn("config watch error", "err", err)
			case <-timer.C:
				data, err := os.ReadFile(path)
				if err != nil {
					slog.Debug("config file unreadable, ignoring", "err", err)
					continue
				}
				if known && bytes.Equal(data, last) {
					continue
				}
				last, known = data, true
				onChange(Parse(data))
			}
		}
	}()
	return nil
}
