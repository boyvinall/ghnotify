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
	cfg    *config.NotificationConfig
	snooze *poller.SnoozeStore
}

// NewNotifier creates a Notifier backed by cfg and snooze. cfg may be mutated
// at runtime (e.g., from Preferences) as long as reads are not concurrent with
// writes; for v1 this is safe because prefs are only changed at app restart.
func NewNotifier(cfg *config.NotificationConfig, snooze *poller.SnoozeStore) *Notifier {
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
	if err := beeep.Notify(title, body, ""); err != nil {
		log.Printf("ghnotify: notification: %v", err)
	}
}

func (n *Notifier) format(c poller.Change) (title, body string, ok bool) {
	pr := c.PR
	repo := fmt.Sprintf("%s › %s/%s", pr.Server, pr.Owner, pr.Repo)

	switch c.Kind {
	case poller.ChangeAdded:
		if c.Category == poller.CategoryReviewRequest && n.cfg.NewReviewRequests {
			return repo,
				fmt.Sprintf("Review requested on #%d: %s", pr.Number, pr.Title),
				true
		}

	case poller.ChangeRemoved:
		// ChangeRemoved on my PRs means merged or closed.
		if c.Category == poller.CategoryMyPR && n.cfg.PRMerged {
			return repo,
				fmt.Sprintf("#%d closed/merged: %s", pr.Number, pr.Title),
				true
		}

	case poller.ChangeReview:
		if c.Category == poller.CategoryMyPR && n.cfg.PRApproved {
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
		if n.cfg.CIStatusChange {
			return repo,
				fmt.Sprintf("#%d CI %s — %s", pr.Number, ciLabel(pr.CIStatus), pr.Title),
				true
		}

	case poller.ChangeComments:
		if n.cfg.NewComments {
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
