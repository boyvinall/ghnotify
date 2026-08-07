# Add "Subscribed PRs" section (via GitHub Notifications API)

## Context

ghnotify currently tracks two PR categories per host: PRs you authored (`FetchMyPRs`,
via GitHub search `author:@me`) and PRs where you're a requested reviewer
(`FetchReviewRequests`, via `review-requested:@me`). The user wants a third
category: PRs they're *subscribed to* (watching for activity) but neither
authored nor been asked to review — e.g. PRs they commented on, were
mentioned in, or manually clicked "Watch" on.

GitHub's search API has no `subscribed:@me` qualifier — "subscribed" is a
notifications concept, not a search one. The correct data source is the
Notifications API (`GET /notifications`, i.e. `Activity.ListNotifications` in
go-github), where each `Notification.Reason` field tells us *why* it fired.
`Reason == "subscribed"` specifically means "you're watching this, not the
author/reviewer/mentioned party" — exactly the third bucket we want.

## Design decisions

- **Filter**: keep only notifications where `Subject.Type == "PullRequest"`
  and `Reason == "subscribed"`.
- **Time bound**: pass the existing `since` cutoff (same `prAgeCutoff()` used
  by the other two fetchers) into `NotificationListOptions.Since`. Use
  `All: true` (not just unread) so the list behaves like a persistent
  "currently subscribed" snapshot rather than an unread inbox, matching how
  `FetchMyPRs`/`FetchReviewRequests` return the full open set every poll.
  `PerPage: 100`, no extra pagination — this matches the existing
  (unpaginated) style of `searchPRs`.
- **Dedup**: a PR can generate multiple notification threads; dedupe by
  owner/repo/number before fetching details.
- **Open-only + exclude-authors filtering**: `searchPRs` gets this for free
  from the search query string (`is:open`, `-author:X`). Notifications have
  no such server-side filter, so we filter client-side after fetching PR
  detail:
  - Add an `IsOpen bool` field to `github.PR` (set from `ghPR.GetState()`
    inside `fetchPRDetail` — harmless additive change, also fixes a latent
    edge case where a PR closes between search and detail-fetch).
  - Add `excludeAuthors []string` to `Client` (alongside the existing
    pre-joined `excludeQuery` string used for search) and a helper
    `isExcludedAuthor(login string) bool`. Must handle the bot-login format
    mismatch: config default is `["app/renovate", "app/dependabot"]` (GitHub
    *search* syntax), but the REST API's actual `User.Login` for those is
    `"renovate[bot]"` / `"dependabot[bot]"`. The helper normalizes: strip an
    `"app/"` prefix and also match `<name>[bot]`.
- **Refactor for reuse**: extract the concurrent "fetch PR details for a list
  of refs" logic currently embedded in `searchPRs` into a shared
  `fetchPRRefs(ctx, []prRef) []PR` helper (`prRef{owner, repo, number}`).
  Also simplify `fetchPRDetail`'s signature from `(ctx, owner, repo,
  *ghapi.Issue)` to `(ctx, owner, repo, number int)` since only `.GetNumber()`
  was used — this lets `FetchPRDetail` (used by the `main.go` CLI debug
  command) call it directly without building a fake `Issue`, and lets the new
  notifications path reuse the exact same fetch/aggregate logic (reviews, CI
  status, mergeability) with no duplication.
- **Notification plumbing (`Change.IsReview` → `Change.Category`)**: replace
  the bool with a 3-way enum `Category` (`CategoryMyPR`,
  `CategoryReviewRequest`, `CategorySubscribed`) in `poller.Change`, threaded
  through `stateStore.diff`. In `notify.go`, `ChangeCIStatus` and
  `ChangeComments` already fire regardless of category (no code change
  needed there) — only `ChangeAdded`/`ChangeRemoved`/`ChangeReview` checks
  need their bool test swapped for `c.Category == CategoryReviewRequest` /
  `CategoryMyPR` respectively, preserving today's exact behavior for the two
  existing categories. Subscribed-PR adds/removes/review-changes won't emit
  a desktop notification (no existing config toggle covers that semantic,
  and inventing one is out of scope) — but CI-status and new-comment
  notifications, and the tray list itself, work for subscribed PRs from day
  one.

## Files to change

1. **`internal/github/client.go`** — add `excludeAuthors []string` field,
   populate in `NewClient`, add `isExcludedAuthor` helper.

2. **`internal/github/types.go`** — add `IsOpen bool` to `PR` struct.

3. **`internal/github/prs.go`**:
   - Add `prRef{owner, repo string; number int}` type.
   - Extract `fetchPRRefs(ctx, []prRef) []PR` from `searchPRs`'s existing
     goroutine/semaphore/reorder logic (unchanged behavior, just
     parameterized on refs instead of `ghapi.Issue`s).
   - Change `fetchPRDetail` signature to take `number int` instead of
     `*ghapi.Issue`; set `IsOpen: ghPR.GetState() == "open"` in the returned
     `PR`.
   - Update `FetchPRDetail` (public) to call `fetchPRDetail` directly.
   - Update `searchPRs` to build `[]prRef` from search results and call
     `fetchPRRefs`.
   - Add `FetchSubscribedPRs(ctx, since time.Time) ([]PR, error)`: calls
     `c.inner.Activity.ListNotifications`, filters/dedupes as above, calls
     `fetchPRRefs`, then filters out `!pr.IsOpen` and
     `c.isExcludedAuthor(pr.Author)`.
   - Small helper to parse the trailing `/{number}` off
     `Notification.Subject.URL`.

4. **`internal/poller/state.go`**:
   - Add `Category` type + constants (`CategoryMyPR`, `CategoryReviewRequest`,
     `CategorySubscribed`).
   - Replace `Change.IsReview bool` with `Change.Category Category`.
   - Add `subscribed map[string]github.PR` to `stateStore`; wire into
     `Update` (new `newSubscribed []github.PR` param + third `diff` call),
     `RemoveHost`, and a new `SubscribedPRs()` accessor (mirrors `MyPRs()`/
     `ReviewRequests()`).
   - `diff` takes `category Category` instead of `isReview bool`.

5. **`internal/poller/poller.go`**:
   - In `pollServer`, add a third fetch: `subscribed, err :=
     client.FetchSubscribedPRs(tctx, since)` (logged the same way as the
     other two), pass into `m.store.Update(host, myPRs, reviews,
     subscribed)`.
   - Add `Manager.SubscribedPRs()` mirroring `MyPRs()`/`ReviewRequests()`
     (`filterByAge(m.store.SubscribedPRs(), m.prAgeCutoff())`).

6. **`internal/notify/notify.go`** — swap the three `c.IsReview` /
   `!c.IsReview` checks for `c.Category == poller.CategoryReviewRequest` /
   `c.Category == poller.CategoryMyPR` as described above.

7. **`internal/tray/tray.go`** — add a third `prList` section ("Subscribed",
   `showApprove: false`, `opts.Poll.SubscribedPRs`), call `.build()` in the
   same place as the other two, include it in `recheck()`'s active-count sum,
   and include `opts.Poll.SubscribedPRs()` in the "Acknowledge All" handler's
   `allPRs` slice.

No config changes needed — `MaxPRsPerSection`, snooze, and acknowledge logic
are already generic across sections.

## Verification

- `go build ./...` and `go vet ./...` to confirm everything compiles.
- Run `go run . pr-detail <some-open-PR-URL>` (the existing CLI debug command
  in `main.go`) before/after the `fetchPRDetail` signature change to confirm
  it still returns full detail (now with an added `isOpen` field in the JSON
  output).
- Run the app (`go run .`) against a real authenticated GitHub host, open the
  tray menu, and confirm:
  - A new "Subscribed" section appears with a plausible PR list distinct
    from "My Pull Requests" and "Review Requests" (no duplicates across
    sections is not required/guaranteed by GitHub — a PR can legitimately be
    both a review request and something you're separately subscribed to,
    but our own author/review-request PRs should mostly land in reason
    `"author"`/`"review_requested"` rather than `"subscribed"`, so overlap
    should be rare in practice).
  - Renovate/Dependabot-authored PRs are excluded from the Subscribed list
    by default, confirming the bot-login normalization works.
  - Snoozing/acknowledging a PR in the Subscribed section behaves like the
    other two sections.
  - "Acknowledge All" also clears the Subscribed section's active state.
