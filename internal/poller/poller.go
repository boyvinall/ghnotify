package poller

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/boyvinall/ghnotify/internal/auth"
	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
)

// Manager runs a per-server poll loop and notifies callers of PR state changes.
type Manager struct {
	authMgr *auth.Manager
	cfg     *config.AppConfig
	store   *stateStore

	onChange func([]Change)

	mu       sync.Mutex
	cancels  map[string]context.CancelFunc // per-server cancel
	refresh  map[string]chan struct{}      // per-server manual-refresh trigger
	wg       sync.WaitGroup
	rootCtx  context.Context
	rootStop context.CancelFunc
}

// NewManager creates a Manager. Call Start() to begin polling.
func NewManager(authMgr *auth.Manager, cfg *config.AppConfig) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		authMgr:  authMgr,
		cfg:      cfg,
		store:    newStateStore(),
		cancels:  make(map[string]context.CancelFunc),
		refresh:  make(map[string]chan struct{}),
		rootCtx:  ctx,
		rootStop: cancel,
	}
}

// OnChange registers a callback invoked (synchronously, in the poll goroutine)
// when PR state changes. Register before calling Start.
func (m *Manager) OnChange(fn func([]Change)) {
	m.onChange = fn
}

// MyPRs returns a current snapshot of all tracked PRs authored by the user,
// filtered to exclude PRs older than the configured max age.
func (m *Manager) MyPRs() []github.PR {
	return filterByAge(m.store.MyPRs(), m.prAgeCutoff())
}

// ReviewRequests returns a current snapshot of all tracked review requests,
// filtered to exclude PRs older than the configured max age.
func (m *Manager) ReviewRequests() []github.PR {
	return filterByAge(m.store.ReviewRequests(), m.prAgeCutoff())
}

// Start launches poll goroutines for all gh-authenticated hosts.
func (m *Manager) Start() {
	servers := m.authMgr.Servers()
	slog.Debug("starting poller", "servers", len(servers))
	for _, s := range servers {
		m.startServer(s.Host)
	}
}

// StartServer begins polling a server that was added after Start.
func (m *Manager) StartServer(host string) {
	m.startServer(host)
}

// StopServer stops the poll loop for host and clears its state.
func (m *Manager) StopServer(host string) {
	m.mu.Lock()
	if cancel, ok := m.cancels[host]; ok {
		cancel()
		delete(m.cancels, host)
	}
	delete(m.refresh, host)
	m.mu.Unlock()
	m.store.RemoveHost(host)
}

// Stop shuts down all poll loops and waits for them to finish.
func (m *Manager) Stop() {
	m.rootStop()
	m.wg.Wait()
}

// Refresh signals every running server loop to poll immediately, bypassing
// the poll interval timer. Safe to call even if a refresh is already pending.
func (m *Manager) Refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.refresh {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// --- internal ----------------------------------------------------------------

func (m *Manager) startServer(host string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, running := m.cancels[host]; running {
		return
	}
	slog.Debug("starting server poll loop", "host", host)
	ctx, cancel := context.WithCancel(m.rootCtx)
	m.cancels[host] = cancel
	refresh := make(chan struct{}, 1)
	m.refresh[host] = refresh

	m.wg.Add(1)
	go m.runServerLoop(ctx, host, refresh)
}

func (m *Manager) runServerLoop(ctx context.Context, host string, refresh <-chan struct{}) {
	defer m.wg.Done()

	interval := m.pollInterval()
	slog.Debug("poll loop started", "host", host, "interval", interval)

	// Poll immediately on start.
	m.pollServer(ctx, host)

	for {
		select {
		case <-ctx.Done():
			slog.Debug("poll loop stopped", "host", host)
			return
		case <-refresh:
			slog.Debug("manual refresh requested", "host", host)
			m.pollServer(ctx, host)
		case <-time.After(jitter(interval)):
			slog.Debug("poll interval elapsed", "host", host)
			m.pollServer(ctx, host)
		}
	}
}

func (m *Manager) pollServer(ctx context.Context, host string) {
	token, err := m.authMgr.GetToken(host)
	if err != nil || token == "" {
		return // not authenticated; skip silently
	}

	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	slog.Debug("polling server", "host", host)
	client := github.NewClient(host, token, m.cfg.ExcludeAuthors)

	since := m.prAgeCutoff()

	myPRs, err := client.FetchMyPRs(tctx, since)
	if err != nil {
		slog.Error("poll failed", "host", host, "query", "my PRs", "err", err)
	}

	reviews, err := client.FetchReviewRequests(tctx, since)
	if err != nil {
		slog.Error("poll failed", "host", host, "query", "review requests", "err", err)
	}

	changes := m.store.Update(host, myPRs, reviews)
	slog.Debug("poll complete", "host", host, "myPRs", len(myPRs), "reviewRequests", len(reviews), "changes", len(changes))
	if len(changes) > 0 && m.onChange != nil {
		m.onChange(changes)
	}
}

func (m *Manager) pollInterval() time.Duration {
	d, err := time.ParseDuration(m.cfg.PollInterval)
	if err != nil || d < 10*time.Second {
		return 300 * time.Second
	}
	return d
}

func jitter(d time.Duration) time.Duration {
	spread := int64(float64(d) * 0.1)
	if spread == 0 {
		return d
	}
	return d + time.Duration(rand.Int63n(spread*2)-spread)
}

// prAgeCutoff returns the earliest UpdatedAt time a PR must have to be shown.
// Returns zero time if no max age is configured.
func (m *Manager) prAgeCutoff() time.Time {
	age := m.cfg.ParseMaxPRAge()
	if age <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-age)
}

func filterByAge(prs []github.PR, cutoff time.Time) []github.PR {
	if cutoff.IsZero() {
		return prs
	}
	out := prs[:0:0]
	for _, pr := range prs {
		if !pr.UpdatedAt.Before(cutoff) {
			out = append(out, pr)
		}
	}
	return out
}
