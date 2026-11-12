package tray

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"github.com/boyvinall/ghnotify/internal/auth"
	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/notify"
	"github.com/boyvinall/ghnotify/internal/poller"
	"github.com/boyvinall/ghnotify/internal/updater"
)

// Options bundles all dependencies for the tray.
type Options struct {
	Config      *config.Store
	ConfigErr   error  // non-nil if config.toml could not be loaded at startup
	ConfigRaw   []byte // config.toml content the startup config was built from
	Auth        *auth.Manager
	Poll        *poller.Manager
	Snooze      *poller.SnoozeStore
	Acknowledge *poller.AcknowledgeStore
	Notif       *notify.Notifier
	Updater     *updater.Updater
}

// Run blocks until the user quits the app. Call from main().
func Run(opts Options) {
	systray.Run(onReady(opts), onExit)
}

func newSectionHeader(label string) *systray.MenuItem {
	systray.AddSeparator()
	m := systray.AddMenuItem(label, "")
	m.Disable()
	return m
}

func onReady(opts Options) func() {
	return func() {
		slog.Debug("setting up tray menu")

		status := &appStatus{}
		status.render()
		systray.SetTooltip("ghnotify — GitHub PR monitor")

		// My PRs section — all slots created BEFORE the separator.
		// Menu slots are created once, so max_prs_per_section needs a restart.
		maxPRs := opts.Config.Get().MaxPRsPerSection
		myList := newPRList(maxPRs, opts.Auth, opts.Snooze, opts.Acknowledge, opts.Config, "My Pull Requests", false, opts.Poll.MyPRs)
		myList.build()

		systray.AddSeparator()

		// Review Requests section — all slots created BEFORE the separator.
		enableApprovePR := false
		reviewList := newPRList(maxPRs, opts.Auth, opts.Snooze, opts.Acknowledge, opts.Config, "Review Requests", enableApprovePR, opts.Poll.ReviewRequests)
		reviewList.build()

		newSectionHeader("Items")

		mAckAll := systray.AddMenuItem("Acknowledge All", "Dismiss active icon until next change")
		mClearSnooze := systray.AddMenuItem("Clear Snoozed Items", "Unsnooze all snoozed PRs")
		mRefresh := systray.AddMenuItem("Refresh Now", "Check GitHub for updates immediately")

		newSectionHeader("App")
		mConfigStatus := systray.AddMenuItem("", "")
		status.item = mConfigStatus
		status.render()
		mPrefs := systray.AddMenuItem("Preferences…", "Open config file")
		mUpdate := systray.AddMenuItem("Check for updates", "")
		mQuit := systray.AddMenuItem("Quit", "Quit ghnotify")

		// Track latest update URL so the click handler knows what to do.
		var latestURL string

		// Start the background update checker.
		ctx, cancelUpdater := context.WithCancel(context.Background())
		opts.Updater.Start(ctx, func(tag, downloadURL string) {
			latestURL = downloadURL
			mUpdate.SetTitle(fmt.Sprintf("Update available: %s  →", tag))
		})

		// recheck re-renders both PR lists and updates the icon. Called on every
		// poll change and on the 60-second ticker to catch snooze expiry.
		var recheckMu sync.Mutex
		recheck := func() {
			recheckMu.Lock()
			defer recheckMu.Unlock()
			_, myActive := myList.update()
			_, revActive := reviewList.update()
			status.setActivity(myActive+revActive > 0)
		}

		// Apply config.toml edits as they are saved.
		reloader := &configReloader{store: opts.Config, status: status, builtAt: maxPRs, refresh: opts.Poll.Refresh, recheck: recheck,
			rebaseAck: func(old *config.NotificationConfig) {
				opts.Acknowledge.Rebaseline(append(opts.Poll.MyPRs(), opts.Poll.ReviewRequests()...), old)
			},
		}
		if opts.ConfigErr != nil {
			reloader.startupError(opts.ConfigErr)
		}
		if path, err := config.GetFilePath(); err != nil {
			slog.Warn("config watch disabled", "err", err)
		} else if err := config.Watch(ctx, path, opts.ConfigRaw, reloader.handle); err != nil {
			slog.Warn("config watch disabled", "err", err)
		}

		// Subscribe to poll changes.
		opts.Poll.OnChange(func(changes []poller.Change) {
			slog.Debug("poll changes", "count", len(changes))
			recheck()
			opts.Notif.HandleChanges(changes)
		})

		ticker := time.NewTicker(60 * time.Second)

		go func() {
			for {
				select {
				case <-ticker.C:
					// Proactively recheck so timed snooze expiry reactivates the
					// icon even when GitHub has no new activity.
					recheck()
				case <-mRefresh.ClickedCh:
					opts.Poll.Refresh()
					reopenMenu()
				case <-mAckAll.ClickedCh:
					allPRs := append(opts.Poll.MyPRs(), opts.Poll.ReviewRequests()...)
					opts.Acknowledge.AcknowledgeAll(allPRs)
					recheck()
				case <-mPrefs.ClickedCh:
					openConfig()
				case <-mConfigStatus.ClickedCh:
					if status.needsRestart() {
						if err := relaunch(); err != nil {
							slog.Error("relaunch failed, staying open", "err", err)
							continue
						}
						ticker.Stop()
						cancelUpdater()
						systray.Quit()
						return
					}
					openConfig()
				case <-mClearSnooze.ClickedCh:
					opts.Snooze.ClearAll()
					recheck()
				case <-mUpdate.ClickedCh:
					if latestURL != "" {
						_ = exec.Command("open", latestURL).Start()
					} else {
						opts.Updater.CheckNow()
					}
				case <-mQuit.ClickedCh:
					ticker.Stop()
					cancelUpdater()
					systray.Quit()
					return
				}
			}
		}()
	}
}

func onExit() {}

func openConfig() {
	path, err := config.GetFilePath()
	if err != nil {
		return
	}
	_ = exec.Command("open", path).Start()
}
