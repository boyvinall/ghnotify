package github

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	ghapi "github.com/google/go-github/v72/github"
)

// maxConcurrentPRs caps the number of PRs fetched in parallel per search query.
const maxConcurrentPRs = 5

// FetchMyPRs returns all open pull requests authored by the authenticated user.
// since, if non-zero, restricts results to PRs updated at or after that time.
func (c *Client) FetchMyPRs(ctx context.Context, since time.Time) ([]PR, error) {
	return c.searchPRs(ctx, "is:pr is:open author:@me archived:false", since)
}

// FetchReviewRequests returns all open PRs where the authenticated user is a
// requested reviewer.
// since, if non-zero, restricts results to PRs updated at or after that time.
func (c *Client) FetchReviewRequests(ctx context.Context, since time.Time) ([]PR, error) {
	return c.searchPRs(ctx, "is:pr is:open review-requested:@me archived:false", since)
}

// FetchSubscribedPRs returns all open PRs the authenticated user is
// subscribed to (watching for activity) but neither authored nor is a
// requested reviewer for. This uses the notifications API rather than
// search, since GitHub search has no "subscribed" qualifier.
// since, if non-zero, restricts results to notifications updated at or
// after that time.
func (c *Client) FetchSubscribedPRs(ctx context.Context, since time.Time) ([]PR, error) {
	opts := &ghapi.NotificationListOptions{
		All:         true,
		ListOptions: ghapi.ListOptions{PerPage: 100},
	}
	if !since.IsZero() {
		opts.Since = since
	}
	slog.Debug("listing notifications", "host", c.host)
	notifications, _, err := c.inner.Activity.ListNotifications(ctx, opts)
	if err != nil {
		return nil, err
	}
	slog.Debug("notifications returned", "host", c.host, "count", len(notifications))

	seen := make(map[string]struct{})
	var refs []prRef
	for _, n := range notifications {
		if n.GetReason() != "subscribed" || n.GetSubject().GetType() != "PullRequest" {
			continue
		}
		owner := n.GetRepository().GetOwner().GetLogin()
		repo := n.GetRepository().GetName()
		number := parseTrailingNumber(n.GetSubject().GetURL())
		if owner == "" || repo == "" || number == 0 {
			continue
		}
		key := owner + "/" + repo + "#" + strconv.Itoa(number)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, prRef{owner: owner, repo: repo, number: number})
	}

	prs := c.fetchPRRefs(ctx, refs)
	out := prs[:0]
	for _, pr := range prs {
		if pr.IsOpen && !c.isExcludedAuthor(pr.Author) {
			out = append(out, pr)
		}
	}
	return out, nil
}

// parseTrailingNumber returns the integer after the last "/" in s, or 0 if
// there isn't one. Used to pull a PR number off a notification subject URL.
func parseTrailingNumber(s string) int {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// prRef identifies a pull request to fetch detail for.
type prRef struct {
	owner  string
	repo   string
	number int
}

func (c *Client) searchPRs(ctx context.Context, query string, since time.Time) ([]PR, error) {
	if c.excludeQuery != "" {
		query += " " + c.excludeQuery
	}
	if !since.IsZero() {
		query += " updated:>" + since.UTC().Format("2006-01-02")
	}
	opts := &ghapi.SearchOptions{
		ListOptions: ghapi.ListOptions{PerPage: 100},
	}
	slog.Debug("searching PRs", "host", c.host, "query", query)
	result, _, err := c.inner.Search.Issues(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	slog.Debug("search returned issues", "host", c.host, "count", result.GetTotal())

	refs := make([]prRef, 0, len(result.Issues))
	for _, issue := range result.Issues {
		owner, repo := parseOwnerRepo(issue.GetRepositoryURL())
		if owner == "" {
			continue
		}
		refs = append(refs, prRef{owner: owner, repo: repo, number: issue.GetNumber()})
	}

	return c.fetchPRRefs(ctx, refs), nil
}

// fetchPRRefs fetches full detail for each ref concurrently (bounded by
// maxConcurrentPRs) and returns the results in the same order as refs,
// skipping any that failed to fetch.
func (c *Client) fetchPRRefs(ctx context.Context, refs []prRef) []PR {
	type entry struct {
		pr  *PR
		idx int
	}

	sem := make(chan struct{}, maxConcurrentPRs)
	ch := make(chan entry, len(refs))
	var wg sync.WaitGroup

	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, ref prRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.Debug("fetching PR detail", "host", c.host, "owner", ref.owner, "repo", ref.repo, "number", ref.number)
			pr, err := c.fetchPRDetail(ctx, ref.owner, ref.repo, ref.number)
			if err != nil {
				slog.Warn("fetch PR detail failed", "owner", ref.owner, "repo", ref.repo, "number", ref.number, "err", err)
				return
			}

			slog.Debug("fetched PR detail",
				"host", c.host,
				"owner", ref.owner,
				"repo", ref.repo,
				"number", pr.Number,
				"title", pr.Title,
				"draft", pr.IsDraft,
				"ci", pr.CIStatus,
				"review", pr.ReviewState)

			ch <- entry{pr, idx}
		}(i, ref)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	// Collect and re-order by original ref order.
	ordered := make([]*PR, len(refs))
	for e := range ch {
		ordered[e.idx] = e.pr
	}
	var prs []PR
	for _, pr := range ordered {
		if pr != nil {
			prs = append(prs, *pr)
		}
	}
	return prs
}

// FetchPRDetail fetches full detail for a single PR by number.
func (c *Client) FetchPRDetail(ctx context.Context, owner, repo string, number int) (*PR, error) {
	return c.fetchPRDetail(ctx, owner, repo, number)
}

func (c *Client) fetchPRDetail(ctx context.Context, owner, repo string, number int) (*PR, error) {
	// Fetch PR metadata and review list concurrently — neither depends on the other.
	var ghPR *ghapi.PullRequest
	var reviews []*ghapi.PullRequestReview
	var getPRErr, listReviewsErr error

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ghPR, _, getPRErr = c.inner.PullRequests.Get(ctx, owner, repo, number)
	}()
	go func() {
		defer wg.Done()
		reviews, _, listReviewsErr = c.inner.PullRequests.ListReviews(ctx, owner, repo, number, nil)
	}()
	wg.Wait()

	if getPRErr != nil {
		return nil, getPRErr
	}
	if listReviewsErr != nil {
		slog.Warn("list reviews failed", "owner", owner, "repo", repo, "number", number, "err", listReviewsErr)
	}

	sha := ghPR.GetHead().GetSHA()
	ciStatus := c.fetchCIStatus(ctx, owner, repo, sha)

	return &PR{
		Server:       c.host,
		Owner:        owner,
		Repo:         repo,
		Number:       number,
		Title:        ghPR.GetTitle(),
		URL:          ghPR.GetHTMLURL(),
		Author:       ghPR.GetUser().GetLogin(),
		IsOpen:       ghPR.GetState() == "open",
		HeadRef:      ghPR.GetHead().GetRef(),
		HeadSHA:      sha,
		IsDraft:      ghPR.GetDraft(),
		ReviewState:  aggregateReviewState(reviews),
		CIStatus:     ciStatus,
		Merge:        parseMergeability(ghPR),
		CommentCount: ghPR.GetComments() + ghPR.GetReviewComments(),
		UpdatedAt:    ghPR.GetUpdatedAt().Time,
	}, nil
}

// fetchCIStatus aggregates modern check runs for the given SHA.
func (c *Client) fetchCIStatus(ctx context.Context, owner, repo, sha string) CIStatus {
	if sha == "" {
		return CIUnknown
	}

	runs, _, err := c.inner.Checks.ListCheckRunsForRef(ctx, owner, repo, sha,
		&ghapi.ListCheckRunsOptions{ListOptions: ghapi.ListOptions{PerPage: 100}})
	if err != nil {
		runs = nil
	}

	return aggregateCIStatus(repo, runs)
}

// --- aggregation helpers -----------------------------------------------------

func aggregateReviewState(reviews []*ghapi.PullRequestReview) ReviewState {
	// Take the most recent review per reviewer; DISMISSED overrides prior state.
	latest := make(map[string]string)
	for _, r := range reviews {
		login := r.GetUser().GetLogin()
		state := r.GetState()
		switch state {
		case "DISMISSED":
			delete(latest, login)
		case "APPROVED", "CHANGES_REQUESTED":
			latest[login] = state
		}
	}
	hasApproved, hasChanges := false, false
	for _, s := range latest {
		switch s {
		case "APPROVED":
			hasApproved = true
		case "CHANGES_REQUESTED":
			hasChanges = true
		}
	}
	if hasChanges {
		return ReviewChangesRequested
	}
	if hasApproved {
		return ReviewApproved
	}
	return ReviewPending
}

func aggregateCIStatus(repo string, runs *ghapi.ListCheckRunsResults) CIStatus {
	failing, pending, passing := false, false, false

	slog.Debug("aggregating CI status", "repo", repo)

	if runs != nil {
		for _, run := range runs.CheckRuns {
			slog.Debug("check run",
				"repo", repo,
				"name", run.GetName(),
				"status", run.GetStatus(),
				"conclusion", run.GetConclusion(),
			)
			switch run.GetConclusion() {
			case "success", "skipped", "neutral":
				passing = true
			case "failure", "cancelled", "timed_out", "action_required":
				failing = true
			case "": // still running
				switch run.GetStatus() {
				case "in_progress", "queued", "waiting":
					pending = true
				}
			}
		}
	}

	switch {
	case failing:
		return CIFailing
	case pending:
		return CIPending
	case passing:
		return CIPassing
	default:
		return CIUnknown
	}
}

func parseMergeability(pr *ghapi.PullRequest) Mergeability {
	switch pr.GetMergeableState() {
	case "clean":
		return MergeMergeable
	case "dirty":
		return MergeConflicted
	case "blocked", "behind", "unstable":
		return MergeBlocked
	default:
		return MergeUnknown
	}
}

// parseOwnerRepo extracts owner and repo from a GitHub repository URL.
// Works for both github.com and GHE: ".../repos/owner/repo"
func parseOwnerRepo(repoURL string) (owner, repo string) {
	_, rest, ok := strings.Cut(repoURL, "/repos/")
	if !ok {
		return "", ""
	}
	owner, repo, ok = strings.Cut(rest, "/")
	if !ok {
		return "", ""
	}
	return owner, strings.TrimRight(repo, "/")
}
