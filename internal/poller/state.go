package poller

import (
	"sync"

	"github.com/boyvinall/ghnotify/internal/github"
)

// ChangeKind classifies what changed on a PR between two poll cycles.
type ChangeKind int

const (
	ChangeNone      ChangeKind = iota
	ChangeAdded                // PR appeared (new or newly matched the query)
	ChangeRemoved              // PR closed/merged or no longer matches
	ChangeReview               // review state changed
	ChangeCIStatus             // CI result changed
	ChangeMergeable            // merge readiness changed
	ChangeComments             // comment count changed
	ChangeUpdated              // anything else (title, draft flag, etc.)
)

// Change describes one PR mutation detected by a poll cycle.
type Change struct {
	Kind     ChangeKind
	PR       github.PR
	Old      *github.PR // nil for ChangeAdded
	IsReview bool       // true = review request; false = my PR
}

// stateStore keeps the last-known set of PRs for each server and produces a
// diff on each update.
type stateStore struct {
	mu      sync.RWMutex
	myPRs   map[string]github.PR // key: PR.Key()
	reviews map[string]github.PR
}

func newStateStore() *stateStore {
	return &stateStore{
		myPRs:   make(map[string]github.PR),
		reviews: make(map[string]github.PR),
	}
}

// Update replaces stored PRs for host with newMyPRs and newReviews and returns
// the detected changes. A nil argument leaves that set untouched.
func (s *stateStore) Update(host string, newMyPRs, newReviews *[]github.PR) []Change {
	s.mu.Lock()
	defer s.mu.Unlock()

	var changes []Change
	if newMyPRs != nil {
		changes = append(changes, s.diff(s.myPRs, toMap(*newMyPRs), host, false)...)
		replaceHost(s.myPRs, host, *newMyPRs)
	}
	if newReviews != nil {
		changes = append(changes, s.diff(s.reviews, toMap(*newReviews), host, true)...)
		replaceHost(s.reviews, host, *newReviews)
	}
	return changes
}

// MyPRs returns a snapshot of all currently tracked PRs authored by the user.
func (s *stateStore) MyPRs() []github.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return mapValues(s.myPRs)
}

// ReviewRequests returns a snapshot of all currently tracked review requests.
func (s *stateStore) ReviewRequests() []github.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return mapValues(s.reviews)
}

// RemoveHost drops all state for host (called when a server is removed).
func (s *stateStore) RemoveHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replaceHost(s.myPRs, host, nil)
	replaceHost(s.reviews, host, nil)
}

// --- helpers -----------------------------------------------------------------

func (s *stateStore) diff(stored, incoming map[string]github.PR, host string, isReview bool) []Change {
	var changes []Change
	// Added: in incoming but not in stored (for this host).
	for key, pr := range incoming {
		if pr.Server != host {
			continue
		}
		if _, exists := stored[key]; !exists {
			changes = append(changes, Change{Kind: ChangeAdded, PR: pr, IsReview: isReview})
		}
	}
	// Removed or updated: in stored for this host.
	for key, old := range stored {
		if old.Server != host {
			continue
		}
		newPR, exists := incoming[key]
		if !exists {
			changes = append(changes, Change{Kind: ChangeRemoved, PR: old, IsReview: isReview})
			continue
		}
		if kind := whatChanged(old, newPR); kind != ChangeNone {
			cp := old
			changes = append(changes, Change{Kind: kind, PR: newPR, Old: &cp, IsReview: isReview})
		}
	}
	return changes
}

func whatChanged(old, newPR github.PR) ChangeKind {
	switch {
	case old.ReviewState != newPR.ReviewState:
		return ChangeReview
	case old.CIStatus != newPR.CIStatus:
		return ChangeCIStatus
	case old.Merge != newPR.Merge:
		return ChangeMergeable
	case old.CommentCount != newPR.CommentCount:
		return ChangeComments
	case old.IsDraft != newPR.IsDraft || old.Title != newPR.Title:
		return ChangeUpdated
	default:
		return ChangeNone
	}
}

func toMap(prs []github.PR) map[string]github.PR {
	m := make(map[string]github.PR, len(prs))
	for _, pr := range prs {
		m[pr.Key()] = pr
	}
	return m
}

func mapValues(m map[string]github.PR) []github.PR {
	out := make([]github.PR, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func replaceHost(m map[string]github.PR, host string, prs []github.PR) {
	for key, pr := range m {
		if pr.Server == host {
			delete(m, key)
		}
	}
	for _, pr := range prs {
		m[pr.Key()] = pr
	}
}
