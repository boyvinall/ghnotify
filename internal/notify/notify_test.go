package notify

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
	"github.com/boyvinall/ghnotify/internal/poller"
)

func testPR() github.PR {
	return github.PR{
		Server: "github.com", Owner: "o", Repo: "r", Number: 7,
		Title: "T", URL: "https://github.com/o/r/pull/7",
	}
}

func allOn() *config.NotificationConfig {
	return &config.NotificationConfig{
		NewReviewRequests: true, PRApproved: true, PRMerged: true,
		CIStatusChange: true, NewComments: true,
	}
}

func store(n *config.NotificationConfig) *config.Store {
	cfg := config.Default()
	cfg.Notifications = *n
	return config.NewStore(cfg)
}

func TestFormat(t *testing.T) {
	approved, changes := testPR(), testPR()
	approved.ReviewState = github.ReviewApproved
	changes.ReviewState = github.ReviewChangesRequested
	pending := testPR()
	pending.ReviewState = github.ReviewPending
	failing := testPR()
	failing.CIStatus = github.CIFailing

	tests := []struct {
		name     string
		cfg      *config.NotificationConfig
		change   poller.Change
		wantBody string
		wantOK   bool
	}{
		{"review requested", allOn(), poller.Change{Kind: poller.ChangeAdded, PR: testPR(), IsReview: true}, "Review requested on #7: T", true},
		{"added own PR is silent", allOn(), poller.Change{Kind: poller.ChangeAdded, PR: testPR()}, "", false},
		{"review requested disabled", &config.NotificationConfig{}, poller.Change{Kind: poller.ChangeAdded, PR: testPR(), IsReview: true}, "", false},
		{"own PR merged", allOn(), poller.Change{Kind: poller.ChangeRemoved, PR: testPR()}, "#7 closed/merged: T", true},
		{"review PR removed is silent", allOn(), poller.Change{Kind: poller.ChangeRemoved, PR: testPR(), IsReview: true}, "", false},
		{"approved", allOn(), poller.Change{Kind: poller.ChangeReview, PR: approved}, "#7 approved ✓ — T", true},
		{"changes requested", allOn(), poller.Change{Kind: poller.ChangeReview, PR: changes}, "#7 changes requested — T", true},
		{"pending review is silent", allOn(), poller.Change{Kind: poller.ChangeReview, PR: pending}, "", false},
		{"review on review-request PR is silent", allOn(), poller.Change{Kind: poller.ChangeReview, PR: approved, IsReview: true}, "", false},
		{"approval disabled", &config.NotificationConfig{}, poller.Change{Kind: poller.ChangeReview, PR: approved}, "", false},
		{"CI failing", allOn(), poller.Change{Kind: poller.ChangeCIStatus, PR: failing}, "#7 CI failing ✗ — T", true},
		{"CI disabled", &config.NotificationConfig{}, poller.Change{Kind: poller.ChangeCIStatus, PR: failing}, "", false},
		{"comments", allOn(), poller.Change{Kind: poller.ChangeComments, PR: testPR()}, "#7 new activity — T", true},
		{"comments disabled", &config.NotificationConfig{}, poller.Change{Kind: poller.ChangeComments, PR: testPR()}, "", false},
		{"mergeable is silent", allOn(), poller.Change{Kind: poller.ChangeMergeable, PR: testPR()}, "", false},
		{"updated is silent", allOn(), poller.Change{Kind: poller.ChangeUpdated, PR: testPR()}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNotifier(store(tt.cfg), nil)
			title, body, ok := n.format(tt.change)
			if ok != tt.wantOK || body != tt.wantBody {
				t.Fatalf("format() = (%q, %q, %v), want body %q ok %v", title, body, ok, tt.wantBody, tt.wantOK)
			}
			if ok && title != "github.com › o/r" {
				t.Errorf("title = %q", title)
			}
		})
	}
}

func TestCILabel(t *testing.T) {
	tests := map[github.CIStatus]string{
		github.CIPassing: "passing ✓",
		github.CIFailing: "failing ✗",
		github.CIPending: "pending ○",
		github.CIUnknown: "unknown",
	}
	for s, want := range tests {
		if got := ciLabel(s); got != want {
			t.Errorf("ciLabel(%v) = %q, want %q", s, got, want)
		}
	}
}

type posted struct{ id, title, body, url string }

func stubPost(t *testing.T, nativeOK bool) (natives *[]posted, fallbacks *[]posted) {
	t.Helper()
	oldNative, oldFallback := native, fallback
	t.Cleanup(func() { native, fallback = oldNative, oldFallback })
	natives, fallbacks = new([]posted), new([]posted)
	native = func(id, title, body, url string) bool {
		*natives = append(*natives, posted{id, title, body, url})
		return nativeOK
	}
	fallback = func(title, body string, _ any) error {
		*fallbacks = append(*fallbacks, posted{title: title, body: body})
		return nil
	}
	return natives, fallbacks
}

func TestPostFallsBackWhenNativeFails(t *testing.T) {
	natives, fallbacks := stubPost(t, false)
	post("id", "title", "body", "https://x")
	if len(*natives) != 1 || len(*fallbacks) != 1 {
		t.Fatalf("native=%d fallback=%d, want 1 and 1", len(*natives), len(*fallbacks))
	}
	if got := (*fallbacks)[0]; got.title != "title" || got.body != "body" {
		t.Errorf("fallback got %+v", got)
	}
}

func TestPostSkipsFallbackWhenNativeSucceeds(t *testing.T) {
	natives, fallbacks := stubPost(t, true)
	post("id", "title", "body", "https://x")
	if len(*natives) != 1 || len(*fallbacks) != 0 {
		t.Fatalf("native=%d fallback=%d, want 1 and 0", len(*natives), len(*fallbacks))
	}
}

func TestDispatch(t *testing.T) {
	snooze := poller.NewSnoozeStore(filepath.Join(t.TempDir(), "snooze.json"))
	n := NewNotifier(store(allOn()), snooze)

	t.Run("posts id and url", func(t *testing.T) {
		natives, _ := stubPost(t, true)
		n.dispatch(poller.Change{Kind: poller.ChangeComments, PR: testPR()})
		if len(*natives) != 1 {
			t.Fatalf("posted %d, want 1", len(*natives))
		}
		got := (*natives)[0]
		wantID := fmt.Sprintf("github.com/o/r#7/%v", poller.ChangeComments)
		if got.id != wantID || got.url != testPR().URL {
			t.Errorf("got id %q url %q, want id %q url %q", got.id, got.url, wantID, testPR().URL)
		}
	})

	t.Run("id is stable per PR and kind", func(t *testing.T) {
		natives, _ := stubPost(t, true)
		other := testPR()
		other.Number = 8
		other.CIStatus = github.CIFailing
		n.dispatch(poller.Change{Kind: poller.ChangeComments, PR: testPR()})
		n.dispatch(poller.Change{Kind: poller.ChangeComments, PR: testPR()})
		n.dispatch(poller.Change{Kind: poller.ChangeCIStatus, PR: testPR()})
		n.dispatch(poller.Change{Kind: poller.ChangeComments, PR: other})
		ids := (*natives)
		if len(ids) != 4 {
			t.Fatalf("posted %d, want 4", len(ids))
		}
		if ids[0].id != ids[1].id {
			t.Errorf("same PR and kind gave different ids %q, %q", ids[0].id, ids[1].id)
		}
		if ids[0].id == ids[2].id || ids[0].id == ids[3].id {
			t.Errorf("different kind or PR reused id %q", ids[0].id)
		}
	})

	t.Run("snoozed PR is silent", func(t *testing.T) {
		natives, fallbacks := stubPost(t, true)
		pr := testPR()
		pr.Number = 99
		snooze.Snooze(pr.Key(), poller.SnoozeEntry{Mode: poller.SnoozeModeUntilTime, ExpiresAt: time.Now().Add(time.Hour)})
		n.dispatch(poller.Change{Kind: poller.ChangeComments, PR: pr})
		if len(*natives)+len(*fallbacks) != 0 {
			t.Fatal("snoozed PR produced a notification")
		}
	})

	t.Run("disabled event is silent", func(t *testing.T) {
		natives, _ := stubPost(t, true)
		off := NewNotifier(store(&config.NotificationConfig{}), snooze)
		off.dispatch(poller.Change{Kind: poller.ChangeComments, PR: testPR()})
		if len(*natives) != 0 {
			t.Fatal("disabled event produced a notification")
		}
	})
}

func TestAppUsesNativePath(t *testing.T) {
	natives, fallbacks := stubPost(t, true)
	App("title", "body")
	if len(*natives) != 1 || len(*fallbacks) != 0 {
		t.Fatalf("native=%d fallback=%d, want 1 and 0", len(*natives), len(*fallbacks))
	}
	if got := (*natives)[0]; got.id != "" || got.title != "title" || got.body != "body" {
		t.Errorf("native got %+v, want an empty id so each notification is new", got)
	}
}
