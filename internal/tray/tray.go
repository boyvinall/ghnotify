package tray

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
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
	Config      *config.AppConfig
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

func setIcon(active bool) {
	if active {
		systray.SetIcon(iconActiveBytes())
	} else {
		systray.SetIcon(iconBytes())
	}
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

		setIcon(false)
		systray.SetTooltip("ghnotify — GitHub PR monitor")

		// My PRs section — all slots created BEFORE the separator.
		myList := newPRList(opts.Config.MaxPRsPerSection, opts.Auth, opts.Snooze, opts.Acknowledge, &opts.Config.Notifications, "My Pull Requests", false, opts.Poll.MyPRs)
		myList.build()

		systray.AddSeparator()

		// Review Requests section — all slots created BEFORE the separator.
		enableApprovePR := false
		reviewList := newPRList(opts.Config.MaxPRsPerSection, opts.Auth, opts.Snooze, opts.Acknowledge, &opts.Config.Notifications, "Review Requests", enableApprovePR, opts.Poll.ReviewRequests)
		reviewList.build()

		systray.AddSeparator()

		// Subscribed section — all slots created BEFORE the separator.
		subscribedList := newPRList(opts.Config.MaxPRsPerSection, opts.Auth, opts.Snooze, opts.Acknowledge, &opts.Config.Notifications, "Subscribed", enableApprovePR, opts.Poll.SubscribedPRs)
		subscribedList.build()

		newSectionHeader("Items")

		mAckAll := systray.AddMenuItem("Acknowledge All", "Dismiss active icon until next change")
		mClearSnooze := systray.AddMenuItem("Clear Snoozed Items", "Unsnooze all snoozed PRs")
		mRefresh := systray.AddMenuItem("Refresh Now", "Check GitHub for updates immediately")

		newSectionHeader("App")
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
		recheck := func() {
			_, myActive := myList.update()
			_, revActive := reviewList.update()
			_, subActive := subscribedList.update()
			setIcon(myActive+revActive+subActive > 0)
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
				case <-mAckAll.ClickedCh:
					allPRs := append(opts.Poll.MyPRs(), opts.Poll.ReviewRequests()...)
					allPRs = append(allPRs, opts.Poll.SubscribedPRs()...)
					opts.Acknowledge.AcknowledgeAll(allPRs)
					recheck()
				case <-mPrefs.ClickedCh:
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
