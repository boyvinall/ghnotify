package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantErr     string
		wantUnknown []string
		check       func(*testing.T, *AppConfig)
	}{
		{
			name: "empty file gives defaults",
			in:   "",
			check: func(t *testing.T, c *AppConfig) {
				if c.PollInterval != "120s" || c.MaxPRsPerSection != 20 {
					t.Errorf("got %+v, want defaults", c)
				}
			},
		},
		{
			name: "override keeps other defaults",
			in:   "poll_interval = \"60s\"\n[notifications]\nnew_comments = false\n",
			check: func(t *testing.T, c *AppConfig) {
				if c.PollInterval != "60s" || c.Notifications.NewComments || !c.Notifications.PRMerged {
					t.Errorf("got %+v", c)
				}
			},
		},
		{name: "syntax error", in: "poll_interval 60s", wantErr: "line 1"},
		{name: "bad poll_interval", in: `poll_interval = "soon"`, wantErr: "poll_interval"},
		{name: "bad max_pr_age", in: `max_pr_age = "a week"`, wantErr: "max_pr_age"},
		{name: "empty max_pr_age is allowed", in: `max_pr_age = ""`},
		{name: "zero slots", in: `max_prs_per_section = 0`, wantErr: "max_prs_per_section"},
		{name: "unknown key", in: `pol_interval = "60s"`, wantUnknown: []string{"pol_interval"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Parse([]byte(tt.in))
			if tt.wantErr != "" {
				if res.Err == nil || !strings.Contains(res.Err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", res.Err, tt.wantErr)
				}
				if res.Config != nil {
					t.Errorf("Config should be nil on error")
				}
				return
			}
			if res.Err != nil {
				t.Fatalf("unexpected err: %v", res.Err)
			}
			if strings.Join(res.Unknown, ",") != strings.Join(tt.wantUnknown, ",") {
				t.Errorf("unknown = %v, want %v", res.Unknown, tt.wantUnknown)
			}
			if tt.check != nil {
				tt.check(t, res.Config)
			}
		})
	}
}

func TestDiff(t *testing.T) {
	a := Default()
	b := Default()
	if d := Diff(a, b); len(d) != 0 {
		t.Fatalf("identical configs: got %v", d)
	}
	b.PollInterval = "60s"
	b.Notifications.NewComments = false
	b.ExcludeAuthors = []string{"app/renovate"}
	got := Diff(a, b)
	want := []string{
		`poll_interval: "120s" → "60s"`,
		`exclude_authors: [app/renovate app/dependabot] → [app/renovate]`,
		`notifications.new_comments: true → false`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`poll_interval = "120s"`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan ParseResult, 10)
	if err := Watch(ctx, path, []byte(`poll_interval = "120s"`), func(r ParseResult) { got <- r }); err != nil {
		t.Fatal(err)
	}

	next := func() ParseResult {
		t.Helper()
		select {
		case r := <-got:
			return r
		case <-time.After(3 * time.Second):
			t.Fatal("no reload reported")
			return ParseResult{}
		}
	}
	none := func() {
		t.Helper()
		select {
		case r := <-got:
			t.Fatalf("unexpected reload: %+v", r)
		case <-time.After(watchDebounce * 3):
		}
	}

	// Saving identical content is not a change.
	if err := os.WriteFile(path, []byte(`poll_interval = "120s"`), 0o600); err != nil {
		t.Fatal(err)
	}
	none()

	// Atomic save (write temp file, rename over) as most editors do.
	tmp := filepath.Join(dir, ".config.toml.swp")
	if err := os.WriteFile(tmp, []byte(`poll_interval = "60s"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	if r := next(); r.Err != nil || r.Config.PollInterval != "60s" {
		t.Fatalf("got %+v", r)
	}

	// Several quick writes are reported once, with the final content.
	for _, v := range []string{"10s", "20s", "30s"} {
		if err := os.WriteFile(path, []byte(`poll_interval = "`+v+`"`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if r := next(); r.Config == nil || r.Config.PollInterval != "30s" {
		t.Fatalf("got %+v", r)
	}
	none()

	// A broken file is reported as an error.
	if err := os.WriteFile(path, []byte(`poll_interval = `), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := next(); r.Err == nil {
		t.Fatalf("want error, got %+v", r)
	}
}

func TestWatchReportsEditMadeBeforeWatching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`poll_interval = "60s"`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan ParseResult, 1)
	loaded := []byte(`poll_interval = "120s"`)
	if err := Watch(ctx, path, loaded, func(r ParseResult) { got <- r }); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.Err != nil || r.Config.PollInterval != "60s" {
			t.Fatalf("got %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("edit made before the watcher started was not reported")
	}
}

func TestLoadReturnsBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := GetFilePath()
	if err != nil {
		t.Fatal(err)
	}

	cfg, raw, err := Load()
	if err != nil || cfg == nil {
		t.Fatalf("first run: cfg=%v err=%v", cfg, err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(onDisk) || len(raw) == 0 {
		t.Errorf("first run baseline %q, want the file content %q", raw, onDisk)
	}

	bad := []byte(`poll_interval = "soon"`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, raw, err = Load()
	if err == nil || cfg == nil || string(raw) != string(bad) {
		t.Errorf("invalid file: cfg=%v raw=%q err=%v", cfg, raw, err)
	}
}

func TestWatchUnknownBaselineIsQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`poll_interval = "60s"`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan ParseResult, 1)
	if err := Watch(ctx, path, nil, func(r ParseResult) { got <- r }); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		t.Fatalf("reported a change at startup with no baseline: %+v", r)
	case <-time.After(watchDebounce * 3):
	}
}

func TestWatchEmptyFileAfterUnknownBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`poll_interval = "60s"`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan ParseResult, 1)
	if err := Watch(ctx, path, nil, func(r ParseResult) { got <- r }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.Err != nil || r.Config.PollInterval != "120s" {
			t.Fatalf("got %+v, want the defaults", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an empty file was ignored because the baseline was unknown")
	}
}
