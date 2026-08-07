package github

import (
	"fmt"
	"time"
)

// ReviewState is the aggregate review outcome for a PR.
type ReviewState int

const (
	ReviewPending          ReviewState = iota // no reviews yet
	ReviewApproved                            // at least one approval, no changes requested
	ReviewChangesRequested                    // at least one reviewer requested changes
)

// CIStatus is the aggregate CI outcome across all checks and statuses.
type CIStatus int

const (
	CIUnknown CIStatus = iota
	CIPending
	CIPassing
	CIFailing
)

// Mergeability represents whether the PR can be merged right now.
type Mergeability int

const (
	MergeUnknown    Mergeability = iota
	MergeMergeable               // clean, all requirements met
	MergeConflicted              // has merge conflicts
	MergeBlocked                 // blocked by branch protection rules
)

// PR is our unified view of a pull request, built from several API calls.
type PR struct {
	Server       string
	Owner        string
	Repo         string
	Number       int
	Title        string
	URL          string
	Author       string
	IsOpen       bool
	IsDraft      bool
	ReviewState  ReviewState
	CIStatus     CIStatus
	Merge        Mergeability
	HeadRef      string
	HeadSHA      string
	CommentCount int
	UpdatedAt    time.Time
}

// Key returns a stable, unique string identifying this PR across servers.
func (p PR) Key() string {
	return fmt.Sprintf("%s/%s/%s#%d", p.Server, p.Owner, p.Repo, p.Number)
}
