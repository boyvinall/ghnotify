package poller

import (
	"testing"

	"github.com/boyvinall/ghnotify/internal/config"
)

func TestDropRequeriedMembership(t *testing.T) {
	in := []Change{
		{Kind: ChangeAdded}, {Kind: ChangeCIStatus},
		{Kind: ChangeRemoved, IsReview: true}, {Kind: ChangeAdded, IsReview: true},
	}
	got := dropRequeriedMembership(in, true, false)
	if len(got) != 3 || got[0].Kind != ChangeCIStatus || !got[1].IsReview || !got[2].IsReview {
		t.Fatalf("my set requeried: %+v", got)
	}
	if got := dropRequeriedMembership(in, false, false); len(got) != 4 {
		t.Fatalf("nothing requeried: %+v", got)
	}
	if len(in) != 4 || in[0].Kind != ChangeAdded {
		t.Fatalf("input was modified: %+v", in)
	}
}

func TestAppliedQueryTracksEachSet(t *testing.T) {
	var a appliedQuery
	if my, rev := a.apply("q1", true, true); my || rev {
		t.Fatal("first poll must not count as requeried")
	}
	if my, rev := a.apply("q2", true, false); !my || rev {
		t.Fatalf("my=%v rev=%v, want my only", my, rev)
	}
	if my, rev := a.apply("q2", true, true); my || !rev {
		t.Fatalf("my=%v rev=%v, want reviews only", my, rev)
	}
	if my, rev := a.apply("q2", true, true); my || rev {
		t.Fatal("settled query must not count as requeried")
	}
}

func TestQueryKey(t *testing.T) {
	base := config.Default()
	same := config.Default()
	if queryKey(base) != queryKey(same) {
		t.Fatal("equal configs gave different keys")
	}
	same.ExcludeAuthors = []string{"app/dependabot", "app/renovate"}
	if queryKey(base) != queryKey(same) {
		t.Fatal("author order must not change the query")
	}
	same.PollInterval = "30s"
	if queryKey(base) != queryKey(same) {
		t.Fatal("poll_interval must not change the query")
	}
	for name, mutate := range map[string]func(*config.AppConfig){
		"exclude_authors": func(c *config.AppConfig) { c.ExcludeAuthors = nil },
		"joined authors":  func(c *config.AppConfig) { c.ExcludeAuthors = []string{"app/renovate,app/dependabot"} },
		"max_pr_age":      func(c *config.AppConfig) { c.MaxPRAge = "24h" },
	} {
		c := config.Default()
		mutate(c)
		if queryKey(base) == queryKey(c) {
			t.Errorf("%s did not change the key", name)
		}
	}
}
