package tray

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"github.com/boyvinall/ghnotify/internal/auth"
	"github.com/boyvinall/ghnotify/internal/github"
	"github.com/boyvinall/ghnotify/internal/poller"
)

// prItem is one slot in a prList. Slots are pre-created and shown/hidden as the
// PR list changes. Each slot owns its full sub-menu (Open, Copy, Approve, Snooze…).
type prItem struct {
	mu       sync.RWMutex
	pr       *github.PR // nil = slot is unused
	mgr      *auth.Manager
	snooze   *poller.SnoozeStore
	onSnooze func()

	// menu items (created once, never destroyed)
	mItem *systray.MenuItem

	mTitle   *systray.MenuItem
	mOpen    *systray.MenuItem
	mCopy    *systray.MenuItem
	mApprove *systray.MenuItem

	mStatusCI     *systray.MenuItem
	mStatusReview *systray.MenuItem
	mStatusMerge  *systray.MenuItem
	mStatusMeta   *systray.MenuItem
	mStatusRef    *systray.MenuItem

	mSnoozeChange *systray.MenuItem
	mSnooze1h     *systray.MenuItem
	mSnooze8h     *systray.MenuItem
	mSnooze24h    *systray.MenuItem
	mSnooze48h    *systray.MenuItem
	mSnooze1w     *systray.MenuItem
}

func newPRItem(mgr *auth.Manager, snooze *poller.SnoozeStore, showApprove bool, onSnooze func()) *prItem {
	it := &prItem{mgr: mgr, snooze: snooze, onSnooze: onSnooze}
	it.mItem = systray.AddMenuItem("", "")
	it.mTitle = it.mItem.AddSubMenuItem("", "")
	it.mTitle.Disable()

	it.mOpen = it.mItem.AddSubMenuItem("Open in Browser", "")
	it.mCopy = it.mItem.AddSubMenuItem("Copy URL", "")
	it.mApprove = it.mItem.AddSubMenuItem("Approve", "")
	if !showApprove {
		it.mApprove.Hide()
	}

	it.mItem.AddSeparator()
	status := it.mItem.AddSubMenuItem("Status…", "")
	status.Disable()
	it.mStatusCI = it.mItem.AddSubMenuItem("", "")
	// it.mStatusCI.SetIcon() for copy/paste CI icons in front of the text
	it.mStatusReview = it.mItem.AddSubMenuItem("", "")
	it.mStatusMerge = it.mItem.AddSubMenuItem("", "")
	it.mStatusMeta = it.mItem.AddSubMenuItem("", "")
	it.mStatusRef = it.mItem.AddSubMenuItem("", "")

	it.mItem.AddSeparator()
	sep := it.mItem.AddSubMenuItem("Snooze…", "")
	sep.Disable()
	it.mSnoozeChange = it.mItem.AddSubMenuItem("Until next change", "")
	it.mSnooze1h = it.mItem.AddSubMenuItem("1 hour", "")
	it.mSnooze8h = it.mItem.AddSubMenuItem("8 hours", "")
	it.mSnooze24h = it.mItem.AddSubMenuItem("24 hours", "")
	it.mSnooze48h = it.mItem.AddSubMenuItem("48 hours", "")
	it.mSnooze1w = it.mItem.AddSubMenuItem("1 week", "")
	it.mItem.Hide()
	go it.listen()
	return it
}

// assign binds a PR to this slot and makes it visible.
func (it *prItem) assign(pr github.PR) {
	it.mu.Lock()
	it.pr = &pr
	it.mu.Unlock()

	it.mTitle.SetTitle(it.formatTitle())
	it.mItem.SetTitle(formatPRTitle(pr))
	it.mItem.SetTooltip("")
	it.mStatusCI.SetTitle(formatCIStatus(pr.CIStatus))
	it.mStatusReview.SetTitle(formatReviewStatus(pr.ReviewState, pr.IsDraft))
	it.mStatusMerge.SetTitle(formatMergeStatus(pr.Merge))
	it.mStatusMeta.SetTitle(formatMeta(pr))
	it.mStatusRef.SetTitle("Branch: " + pr.HeadRef)
	it.mApprove.SetTitle("Approve")
	it.mApprove.Enable()
	it.mItem.Show()
}

func (it *prItem) formatTitle() string {
	pr := it.currentPR()
	if pr == nil {
		return ""
	}
	u, err := url.Parse(pr.URL)
	if err != nil {
		return ""
	}
	return strings.Join([]string{u.Host, u.Path}, "/")
}

// clear hides this slot and detaches the PR.
func (it *prItem) clear() {
	it.mu.Lock()
	it.pr = nil
	it.mu.Unlock()
	it.mItem.Hide()
}

func (it *prItem) currentPR() *github.PR {
	it.mu.RLock()
	defer it.mu.RUnlock()
	return it.pr
}

func (it *prItem) listen() {
	for {
		select {
		case <-it.mItem.ClickedCh:
			if pr := it.currentPR(); pr != nil {
				openTarget(pr.URL)
			}
		case <-it.mOpen.ClickedCh:
			if pr := it.currentPR(); pr != nil {
				openTarget(pr.URL)
			}
		case <-it.mCopy.ClickedCh:
			if pr := it.currentPR(); pr != nil {
				copyToClipboard(pr.URL)
			}
		case <-it.mApprove.ClickedCh:
			if pr := it.currentPR(); pr != nil {
				go it.doApprove(pr)
			}
		case <-it.mSnoozeChange.ClickedCh:
			it.snoozeUntilChange()
		case <-it.mSnooze1h.ClickedCh:
			it.snoozeFor(time.Hour)
		case <-it.mSnooze8h.ClickedCh:
			it.snoozeFor(8 * time.Hour)
		case <-it.mSnooze24h.ClickedCh:
			it.snoozeFor(24 * time.Hour)
		case <-it.mSnooze48h.ClickedCh:
			it.snoozeFor(48 * time.Hour)
		case <-it.mSnooze1w.ClickedCh:
			it.snoozeFor(7 * 24 * time.Hour)
		}
	}
}

func (it *prItem) snoozeFor(d time.Duration) {
	if pr := it.currentPR(); pr != nil {
		it.snooze.Snooze(pr.Key(), poller.SnoozeEntry{
			Mode:      poller.SnoozeModeUntilTime,
			ExpiresAt: time.Now().Add(d),
		})
		if it.onSnooze != nil {
			it.onSnooze()
		}
	}
}

func (it *prItem) snoozeUntilChange() {
	if pr := it.currentPR(); pr != nil {
		it.snooze.Snooze(pr.Key(), poller.SnoozeEntry{
			Mode:                poller.SnoozeModeUntilChange,
			SnapshotRef:         pr.HeadSHA,
			SnapshotCIStatus:    pr.CIStatus,
			SnapshotReviewState: pr.ReviewState,
			SnapshotIsDraft:     pr.IsDraft,
			SnapshotUpdated:     pr.UpdatedAt,
		})
		if it.onSnooze != nil {
			it.onSnooze()
		}
	}
}

func (it *prItem) doApprove(pr *github.PR) {
	token, err := it.mgr.GetToken(pr.Server)
	if err != nil || token == "" {
		log.Printf("ghnotify: approve: no token for %s", pr.Server)
		return
	}
	client := github.NewClient(pr.Server, token, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Approve(ctx, pr.Owner, pr.Repo, pr.Number); err != nil {
		log.Printf("ghnotify: approve %s/%s#%d: %v", pr.Owner, pr.Repo, pr.Number, err)
		return
	}
	it.mApprove.SetTitle("Approved ✓")
	it.mApprove.Disable()
}

// --- formatting --------------------------------------------------------------

func formatPRTitle(pr github.PR) string {
	ci := ciGlyph(pr.CIStatus)
	rev := reviewGlyph(pr.ReviewState, pr.IsDraft)
	title := pr.Title
	if len(title) > 50 {
		title = title[:47] + "…"
	}
	return fmt.Sprintf("%s%s %s #%d %s", ci, rev, pr.Repo, pr.Number, title)
}

func formatCIStatus(s github.CIStatus) string {
	switch s {
	case github.CIPassing:
		return "CI: ✓ passing"
	case github.CIFailing:
		return "CI: ✗ failing"
	case github.CIPending:
		return "CI: ○ pending"
	default:
		return "CI: · unknown"
	}
}

func formatReviewStatus(s github.ReviewState, draft bool) string {
	if draft {
		return "Review: ~ draft"
	}
	switch s {
	case github.ReviewApproved:
		return "Review: ✓ approved"
	case github.ReviewChangesRequested:
		return "Review: ✗ changes requested"
	default:
		return "Review: ○ pending"
	}
}

func formatMergeStatus(m github.Mergeability) string {
	switch m {
	case github.MergeMergeable:
		return "Merge: ✓ mergeable"
	case github.MergeConflicted:
		return "Merge: ✗ conflicts"
	case github.MergeBlocked:
		return "Merge: ○ blocked"
	default:
		return "Merge: · unknown"
	}
}

func formatMeta(pr github.PR) string {
	comments := ""
	if pr.CommentCount == 1 {
		comments = "  · 1 comment"
	} else if pr.CommentCount > 1 {
		comments = fmt.Sprintf("  · %d comments", pr.CommentCount)
	}
	return fmt.Sprintf("Author: %s%s", pr.Author, comments)
}

func ciGlyph(s github.CIStatus) string {
	switch s {
	case github.CIPassing:
		return "✓"
	case github.CIFailing:
		return "✗"
	case github.CIPending:
		return "○"
	default:
		return "·"
	}
}

func reviewGlyph(s github.ReviewState, draft bool) string {
	if draft {
		return "~"
	}
	switch s {
	case github.ReviewApproved:
		return "✓"
	case github.ReviewChangesRequested:
		return "✗"
	default:
		return "○"
	}
}
