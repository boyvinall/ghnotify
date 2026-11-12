package poller

import (
	"testing"

	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
)

func TestRebaselineKeepsAcknowledgmentAcrossPrefChange(t *testing.T) {
	s := NewAcknowledgeStore("")
	pr := github.PR{Server: "h", Owner: "o", Repo: "r", Number: 1, CommentCount: 2}
	s.AcknowledgeAll([]github.PR{pr})

	pr.CommentCount = 5
	off := &config.NotificationConfig{}
	on := &config.NotificationConfig{NewComments: true}
	if !s.IsAcknowledged(pr, off) {
		t.Fatal("comment change must be ignored while new_comments is off")
	}

	s.Rebaseline([]github.PR{pr}, off)
	if !s.IsAcknowledged(pr, on) {
		t.Fatal("turning new_comments on cleared an acknowledgment")
	}

	pr.CommentCount = 6
	if s.IsAcknowledged(pr, on) {
		t.Fatal("a comment made after the rebaseline must clear the acknowledgment")
	}
}

func TestRebaselineSkipsChangedPRs(t *testing.T) {
	s := NewAcknowledgeStore("")
	pr := github.PR{Server: "h", Owner: "o", Repo: "r", Number: 1, CommentCount: 2}
	s.AcknowledgeAll([]github.PR{pr})

	pr.CommentCount = 5
	on := &config.NotificationConfig{NewComments: true}
	s.Rebaseline([]github.PR{pr}, on)
	if s.IsAcknowledged(pr, on) {
		t.Fatal("a PR that had already changed must stay unacknowledged")
	}
}
