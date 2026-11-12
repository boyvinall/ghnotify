package tray

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/getlantern/systray"

	"github.com/boyvinall/ghnotify/internal/auth"
	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
	"github.com/boyvinall/ghnotify/internal/poller"
)

// prList manages one PR section (My PRs or Review Requests) in the menubar.
// Slots are pre-created at build time; Show/Hide is used to manage visibility.
type prList struct {
	mu          sync.Mutex
	header      *systray.MenuItem // "My Pull Requests (N)" — disabled
	slots       []*prItem         // pre-created pool, one per max-allowed PR
	mMore       *systray.MenuItem // "… and N more" (shown when truncated)
	maxItems    int
	label       string // "My Pull Requests" or "Review Requests"
	showApprove bool
	mgr         *auth.Manager
	snooze      *poller.SnoozeStore
	acknowledge *poller.AcknowledgeStore
	cfg         *config.Store
	prevSnoozed map[string]bool
	getPRs      getPRsFunc
	onSnooze    func()
}

type getPRsFunc func() []github.PR

func newPRList(maxItems int, mgr *auth.Manager, snooze *poller.SnoozeStore, ack *poller.AcknowledgeStore, cfg *config.Store, label string, showApprove bool, getPRs getPRsFunc, onSnooze func()) *prList {
	return &prList{
		maxItems:    maxItems,
		label:       label,
		showApprove: showApprove,
		mgr:         mgr,
		snooze:      snooze,
		acknowledge: ack,
		cfg:         cfg,
		prevSnoozed: make(map[string]bool),
		getPRs:      getPRs,
		onSnooze:    onSnooze,
	}
}

// build creates the header and all pre-allocated slot items in the menu.
// Must be called from the systray onReady goroutine, in the correct menu order.
func (l *prList) build() {
	l.header = systray.AddMenuItem(l.label, "")
	l.header.Disable()

	l.slots = make([]*prItem, l.maxItems)
	for i := range l.slots {
		l.slots[i] = newPRItem(l.mgr, l.snooze, l.showApprove, l.onSnooze)
	}

	l.mMore = systray.AddMenuItem("", "")
	l.mMore.Disable()
	l.mMore.Hide()
}

// update re-renders the section with the provided PR list (full server snapshot).
// It filters snoozed PRs, sorts, caps at maxItems, and returns the visible and
// active counts. visible drives the menu header; active drives the tray icon.
func (l *prList) update() (visible, active int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	prs := l.getPRs()

	// Filter snoozed, detect expiry, collect visible PRs.
	visiblePRs := make([]github.PR, 0, len(prs))
	for _, pr := range prs {
		key := pr.Key()
		snoozed := l.snooze.IsSnoozed(pr)

		// When a time-based snooze expires, override any acknowledgment so the
		// snooze expiry always re-activates the icon.
		if l.prevSnoozed[key] && !snoozed {
			l.acknowledge.Clear(key)
		}
		l.prevSnoozed[key] = snoozed

		if snoozed {
			slog.Debug("PR is snoozed, skipping", "key", key)
		} else {
			slog.Debug("PR is visible", "key", key)
			visiblePRs = append(visiblePRs, pr)
		}
	}

	// Sort: most recently updated first.
	sort.Slice(visiblePRs, func(i, j int) bool {
		return visiblePRs[i].UpdatedAt.After(visiblePRs[j].UpdatedAt)
	})

	total := len(visiblePRs)
	display := visiblePRs
	overflow := 0
	if total > l.maxItems {
		display = visiblePRs[:l.maxItems]
		overflow = total - l.maxItems
	}

	// Assign PRs to slots.
	for i, slot := range l.slots {
		if i < len(display) {
			slot.assign(display[i])
		} else {
			slot.clear()
		}
	}

	// "… and N more" overflow indicator.
	if overflow > 0 {
		l.mMore.SetTitle(fmt.Sprintf("… and %d more (open GitHub to see)", overflow))
		l.mMore.Show()
	} else {
		l.mMore.Hide()
	}

	// Update section header with visible count.
	visible = total
	if visible == 0 {
		l.header.SetTitle(l.label)
	} else {
		l.header.SetTitle(fmt.Sprintf("%s  (%d)", l.label, visible))
	}

	// Active count excludes acknowledged PRs.
	for _, pr := range visiblePRs {
		if !l.acknowledge.IsAcknowledged(pr, &l.cfg.Get().Notifications) {
			active++
		}
	}

	return visible, active
}
