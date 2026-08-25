package data

import "time"

// Kind identifies where an item came from.
type Kind int

const (
	KindJiraIssue Kind = iota
	KindPullRequest
	KindGHIssue
)

// Subtask is a Jira subtask summary line.
type Subtask struct {
	Key            string
	Summary        string
	Status         string
	StatusCategory string // "new" | "indeterminate" | "done"
	URL            string
}

// Item is a unified work item shown in the TUI.
type Item struct {
	Kind            Kind
	Key             string // PROJ-123 or owner/repo#42
	Title           string
	Status          string // Jira status name, or PR/issue state
	StatusCategory  string // "new" | "indeterminate" | "done"
	URL             string
	Repo            string
	IssueType       string
	Priority        string
	ReviewRequested bool
	Draft           bool
	Sprint          string
	Subtasks        []Subtask

	// Pull-request detail (GraphQL-sourced).
	Bucket         Bucket
	Author         string
	Additions      int
	Deletions      int
	Updated        time.Time
	CIState        string // "SUCCESS" | "FAILURE" | "ERROR" | "PENDING" | "EXPECTED" | ""
	Mergeable      string // "MERGEABLE" | "CONFLICTING" | "UNKNOWN" | ""
	MergeState     string // mergeStateStatus: CLEAN | BEHIND | BLOCKED | DIRTY | UNSTABLE | …
	Branch         string // PR head branch
	ReviewDecision string // "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | ""
	HeadSHA        string
	Merged         bool
	MergedAt       time.Time
	MergeSHA       string // merge commit on the target branch
	MergeCIState   string // statusCheckRollup of the merge commit (post-merge GHA)

	Description  string // Jira description (plain text)
	CommentCount int    // Jira comments on the issue
}

// PRDetail is lazily-fetched pull-request detail for the detail pane.
type PRDetail struct {
	Body     string
	Branch   string
	Commits  []Commit
	Files    []FileChange
	Checks   []CheckStatus
	Comments []Comment
}

// CheckStatus is one CI check or commit status (build, deploy, …).
type CheckStatus struct {
	Name  string
	State string // SUCCESS, FAILURE, PENDING, …
}

// WorkflowRun is one check suite on a commit — a GitHub Actions workflow run
// or an external app's suite — with its jobs.
type WorkflowRun struct {
	Name       string
	URL        string
	Status     string // QUEUED | IN_PROGRESS | COMPLETED
	Conclusion string // SUCCESS | FAILURE | CANCELLED | SKIPPED | …
	Created    time.Time
	Jobs       []CheckRun
}

// CheckRun is one job inside a workflow run.
type CheckRun struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
	Started    time.Time
	Completed  time.Time
}

// BranchRef is a branch found in a local clone (used to surface work that
// has a branch but no PR yet).
type BranchRef struct {
	Repo    string // local repo folder name
	RepoDir string // absolute path to the clone
	Name    string // branch name
}

// Comment is a Jira issue comment or a PR conversation comment.
type Comment struct {
	Author  string
	Body    string
	Created time.Time
}

type Commit struct {
	SHA     string
	Message string
	Author  string
	Date    time.Time
}

type FileChange struct {
	Path      string
	Additions int
	Deletions int
}

// Bucket is a GitKraken-Launchpad-style action group for pull requests.
type Bucket int

const (
	BucketNone Bucket = iota
	BucketNeedsMyReview
	BucketWaitingForReview
	BucketReadyToMerge
	BucketResolveConflicts
	BucketFailingCI
	BucketReviewerCommented
	BucketUnassignedReviewers
	BucketDraft
	BucketOther
)

// BucketOrder is the display order of PR groups.
var BucketOrder = []Bucket{
	BucketNeedsMyReview,
	BucketWaitingForReview,
	BucketReadyToMerge,
	BucketResolveConflicts,
	BucketFailingCI,
	BucketReviewerCommented,
	BucketUnassignedReviewers,
	BucketDraft,
	BucketOther,
}

func (b Bucket) Label() string {
	switch b {
	case BucketNeedsMyReview:
		return "Needs My Review"
	case BucketWaitingForReview:
		return "Waiting for Review"
	case BucketReadyToMerge:
		return "Ready to Merge"
	case BucketResolveConflicts:
		return "Resolve Conflicts"
	case BucketFailingCI:
		return "Failing CI"
	case BucketReviewerCommented:
		return "Reviewer Commented"
	case BucketUnassignedReviewers:
		return "Unassigned Reviewers"
	case BucketDraft:
		return "Draft"
	default:
		return "Other"
	}
}
