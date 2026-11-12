package poller

import (
	"testing"

	"github.com/boyvinall/ghnotify/internal/github"
)

func TestUpdateNilKeepsStoredSet(t *testing.T) {
	const host = "github.com"
	mine := []github.PR{{Server: host, Owner: "o", Repo: "r", Number: 1}}
	reviews := []github.PR{{Server: host, Owner: "o", Repo: "r", Number: 2}}
	s := newStateStore()
	s.Update(host, &mine, &reviews)

	if changes := s.Update(host, nil, &reviews); len(changes) != 0 {
		t.Fatalf("failed my-PRs fetch produced changes: %v", changes)
	}
	if got := s.MyPRs(); len(got) != 1 {
		t.Fatalf("MyPRs = %d, want 1", len(got))
	}

	empty := []github.PR{}
	changes := s.Update(host, &mine, &empty)
	if len(changes) != 1 || changes[0].Kind != ChangeRemoved || !changes[0].IsReview {
		t.Fatalf("changes = %v, want one removed review", changes)
	}
}
