package poller

import (
	"testing"

	"github.com/boyvinall/ghnotify/internal/config"
)

func TestDropMembership(t *testing.T) {
	in := []Change{{Kind: ChangeAdded}, {Kind: ChangeRemoved}, {Kind: ChangeCIStatus}, {Kind: ChangeRemoved}}
	got := dropMembership(in)
	if len(got) != 1 || got[0].Kind != ChangeCIStatus {
		t.Fatalf("dropMembership = %+v", got)
	}
	if len(in) != 4 || in[1].Kind != ChangeRemoved {
		t.Fatalf("input was modified: %+v", in)
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
