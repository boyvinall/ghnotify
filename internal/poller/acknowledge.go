package poller

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
)

// AcknowledgeSnapshot records the PR state at the time it was acknowledged.
type AcknowledgeSnapshot struct {
	HeadSHA      string             `json:"head_sha"`
	ReviewState  github.ReviewState `json:"review_state"`
	CIStatus     github.CIStatus    `json:"ci_status"`
	CommentCount int                `json:"comment_count"`
	IsDraft      bool               `json:"is_draft"`
}

// AcknowledgeStore persists per-PR acknowledgment state to a JSON file.
// Acknowledged PRs remain visible in the menu but do not contribute to the
// active icon state until a notification-relevant field changes.
type AcknowledgeStore struct {
	mu      sync.Mutex
	entries map[string]AcknowledgeSnapshot
	path    string
}

// NewAcknowledgeStore loads existing acknowledge state from path (creates an
// empty store if the file does not exist yet).
func NewAcknowledgeStore(path string) *AcknowledgeStore {
	s := &AcknowledgeStore{entries: make(map[string]AcknowledgeSnapshot), path: path}
	s.load()
	return s
}

// AcknowledgeAll snapshots the current state of every PR in the list.
func (s *AcknowledgeStore) AcknowledgeAll(prs []github.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range prs {
		s.entries[pr.Key()] = snapshotOf(pr)
	}
	s.save()
}

func snapshotOf(pr github.PR) AcknowledgeSnapshot {
	return AcknowledgeSnapshot{
		HeadSHA:      pr.HeadSHA,
		ReviewState:  pr.ReviewState,
		CIStatus:     pr.CIStatus,
		CommentCount: pr.CommentCount,
		IsDraft:      pr.IsDraft,
	}
}

func (snap AcknowledgeSnapshot) differsFrom(pr github.PR, cfg *config.NotificationConfig) bool {
	return snap.HeadSHA != pr.HeadSHA ||
		snap.IsDraft != pr.IsDraft ||
		(cfg.PRApproved && snap.ReviewState != pr.ReviewState) ||
		(cfg.CIStatusChange && snap.CIStatus != pr.CIStatus) ||
		(cfg.NewComments && snap.CommentCount != pr.CommentCount)
}

// Rebaseline re-snapshots every PR in prs that is acknowledged under cfg.
// Call it before the notification preferences change, so a field that was
// ignored until now does not suddenly count as a change and clear the
// acknowledgment.
func (s *AcknowledgeStore) Rebaseline(prs []github.PR, cfg *config.NotificationConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := false
	for _, pr := range prs {
		snap, ok := s.entries[pr.Key()]
		if !ok || snap.differsFrom(pr, cfg) {
			continue
		}
		if now := snapshotOf(pr); now != snap {
			s.entries[pr.Key()] = now
			updated = true
		}
	}
	if updated {
		s.save()
	}
}

// IsAcknowledged reports whether pr is still acknowledged. It auto-clears the
// entry and returns false if a notification-relevant field has changed since
// acknowledgment. HeadSHA and IsDraft are always checked; other fields are
// gated on the corresponding notification preference.
func (s *AcknowledgeStore) IsAcknowledged(pr github.PR, cfg *config.NotificationConfig) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.entries[pr.Key()]
	if !ok {
		return false
	}
	if snap.differsFrom(pr, cfg) {
		delete(s.entries, pr.Key())
		s.save()
		return false
	}
	return true
}

// Clear removes the acknowledgment entry for a single PR key.
// Called when a time-based snooze expires so the snooze expiry overrides any
// outstanding acknowledgment.
func (s *AcknowledgeStore) Clear(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; ok {
		delete(s.entries, key)
		s.save()
	}
}

// ClearAll removes all acknowledgment entries.
func (s *AcknowledgeStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]AcknowledgeSnapshot)
	s.save()
}

// --- persistence -------------------------------------------------------------

type acknowledgeFile struct {
	Acknowledged map[string]AcknowledgeSnapshot `json:"acknowledged"`
}

func (s *AcknowledgeStore) save() {
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(acknowledgeFile{Acknowledged: s.entries}, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		log.Printf("ghnotify: save acknowledge: %v", err)
	}
}

func (s *AcknowledgeStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f acknowledgeFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("ghnotify: load acknowledge: %v", err)
		return
	}
	if f.Acknowledged != nil {
		s.entries = f.Acknowledged
	}
}
