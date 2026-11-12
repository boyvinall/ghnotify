// Package notify dispatches OS-level notifications for PR state changes,
// gated by the user's per-event preferences in config.NotificationConfig.
package notify

import (
	"fmt"
	"log"

	"github.com/gen2brain/beeep"

	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
	"github.com/boyvinall/ghnotify/internal/poller"
)

// Notifier fires OS notifications for PR changes that the user has opted into.
type Notifier struct {
	cfg    *config.Store
	snooze *poller.SnoozeStore
}

// NewNotifier creates a Notifier backed by cfg and snooze. The notification
// preferences are read from cfg on every change, so a config reload applies
// immediately.
func NewNotifier(cfg *config.Store, snooze *poller.SnoozeStore) *Notifier {
	return &Notifier{cfg: cfg, snooze: snooze}
}

// HandleChanges processes a batch of changes from one poll cycle.
func (n *Notifier) HandleChanges(changes []poller.Change) {
	for _, c := range changes {
		n.dispatch(c)
	}
}

func (n *Notifier) dispatch(c poller.Change) {
	if n.snooze.IsSnoozed(c.PR) {
		return
	}
	title, body, ok := n.format(c)
	if !ok {
		return
	}
	id := fmt.Sprintf("%s/%s/%s#%d/%v", c.PR.Server, c.PR.Owner, c.PR.Repo, c.PR.Number, c.Kind)
	post(id, title, body, c.PR.URL)
}

var (
	native   = nativePost
	fallback = beeep.Notify
)

// post shows a notification. Clicking it opens url, when set and supported.
// On macOS it uses the native notification center when running from the .app
// bundle; otherwise it falls back to beeep.
func post(id, title, body, url string) {
	if native(id, title, body, url) {
		return
	}
	if err := fallback(title, body, ""); err != nil {
		log.Printf("ghnotify: notification: %v", err)
	}
}

func (n *Notifier) format(c poller.Change) (title, body string, ok bool) {
	pr := c.PR
	prefs := n.cfg.Get().Notifications
	repo := fmt.Sprintf("%s › %s/%s", pr.Server, pr.Owner, pr.Repo)

	switch c.Kind {
	case poller.ChangeAdded:
		if c.IsReview && prefs.NewReviewRequests {
			return repo,
				fmt.Sprintf("Review requested on #%d: %s", pr.Number, pr.Title),
				true
		}

	case poller.ChangeRemoved:
		// ChangeRemoved on my PRs means merged or closed.
		if !c.IsReview && prefs.PRMerged {
			return repo,
				fmt.Sprintf("#%d closed/merged: %s", pr.Number, pr.Title),
				true
		}

	case poller.ChangeReview:
		if !c.IsReview && prefs.PRApproved {
			switch pr.ReviewState {
			case github.ReviewApproved:
				return repo,
					fmt.Sprintf("#%d approved ✓ — %s", pr.Number, pr.Title),
					true
			case github.ReviewChangesRequested:
				return repo,
					fmt.Sprintf("#%d changes requested — %s", pr.Number, pr.Title),
					true
			}
		}

	case poller.ChangeCIStatus:
		if prefs.CIStatusChange {
			return repo,
				fmt.Sprintf("#%d CI %s — %s", pr.Number, ciLabel(pr.CIStatus), pr.Title),
				true
		}

	case poller.ChangeComments:
		if prefs.NewComments {
			return repo,
				fmt.Sprintf("#%d new activity — %s", pr.Number, pr.Title),
				true
		}
	}
	return "", "", false
}

func ciLabel(s github.CIStatus) string {
	switch s {
	case github.CIPassing:
		return "passing ✓"
	case github.CIFailing:
		return "failing ✗"
	case github.CIPending:
		return "pending ○"
	default:
		return "unknown"
	}
}

// App posts a notification about ghnotify itself, such as a config reload.
// It is not gated by any preference: these only fire after the user acts.
// It blocks until the notification is posted or times out.
func App(title, body string) {
	post("", title, body, "")
}
