package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/jirafmt"
)

// Fetch returns open PRs (authored + review-requested), recently merged PRs
// (for issue linking and post-merge CI), and open issues assigned to the user.
func Fetch(ctx context.Context, g *config.GitHub) (prs, merged, issues []data.Item, err error) {
	token, err := g.ResolveToken()
	if err != nil {
		return nil, nil, nil, err
	}
	login, err := viewerLogin(ctx, g, token)
	if err != nil {
		return nil, nil, nil, err
	}

	scopes := scopeQualifiers(g)
	seen := map[string]bool{}
	since := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	for _, scope := range scopes {
		authoredQ := strings.TrimSpace(fmt.Sprintf("is:open is:pr archived:false author:%s %s", login, scope))
		reviewQ := strings.TrimSpace(fmt.Sprintf("is:open is:pr archived:false review-requested:%s %s", login, scope))
		// sort:updated-desc matters: without it search returns an arbitrary
		// "relevant" 50 of possibly hundreds, dropping recent merges.
		mergedQ := strings.TrimSpace(fmt.Sprintf("is:pr is:merged archived:false merged:>=%s sort:updated-desc %s", since, scope))
		// Busy orgs merge more than 50 PRs in days, pushing the user's own
		// older sprint merges off the recent-50 — a dedicated author query
		// keeps their PRs linked regardless of org volume.
		mergedMineQ := strings.TrimSpace(fmt.Sprintf("is:pr is:merged archived:false author:%s merged:>=%s sort:updated-desc %s", login, since, scope))

		var resp struct {
			Authored   searchNodes `json:"authored"`
			Review     searchNodes `json:"review"`
			MergedPR   searchNodes `json:"mergedPRs"`
			MergedMine searchNodes `json:"mergedMine"`
		}
		vars := map[string]any{"authored": authoredQ, "review": reviewQ, "merged": mergedQ, "mergedMine": mergedMineQ}
		if err := gql(ctx, g, token, prSearchQuery, vars, &resp); err != nil {
			return nil, nil, nil, err
		}
		for _, n := range resp.Review.Nodes {
			if n.URL == "" || seen[n.URL] {
				continue
			}
			seen[n.URL] = true
			prs = append(prs, toPRItem(n, true))
		}
		for _, n := range resp.Authored.Nodes {
			if n.URL == "" || seen[n.URL] {
				continue
			}
			seen[n.URL] = true
			prs = append(prs, toPRItem(n, false))
		}
		for _, n := range append(resp.MergedPR.Nodes, resp.MergedMine.Nodes...) {
			if n.URL == "" || seen[n.URL] {
				continue
			}
			seen[n.URL] = true
			merged = append(merged, toPRItem(n, false))
		}
	}
	sort.SliceStable(prs, func(a, b int) bool {
		return prs[a].Updated.After(prs[b].Updated)
	})
	sort.SliceStable(merged, func(a, b int) bool {
		return merged[a].MergedAt.After(merged[b].MergedAt)
	})

	for _, scope := range scopes {
		q := strings.TrimSpace("is:open is:issue assignee:@me archived:false " + scope)
		res, err := restSearch(ctx, g, token, q)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, it := range res.Items {
			if seen[it.HTMLURL] {
				continue
			}
			seen[it.HTMLURL] = true
			repo := repoFromURL(it.RepositoryURL)
			issues = append(issues, data.Item{
				Kind:           data.KindGHIssue,
				Key:            fmt.Sprintf("%s#%d", repo, it.Number),
				Title:          it.Title,
				Status:         it.State,
				StatusCategory: "new",
				URL:            it.HTMLURL,
				Repo:           repo,
			})
		}
	}
	return prs, merged, issues, nil
}

// ---- GraphQL ----

const prSearchQuery = `
query($authored: String!, $review: String!, $merged: String!, $mergedMine: String!) {
  authored: search(query: $authored, type: ISSUE, first: 50) { nodes { ...pr } }
  review: search(query: $review, type: ISSUE, first: 50) { nodes { ...pr } }
  mergedPRs: search(query: $merged, type: ISSUE, first: 50) { nodes { ...prm } }
  mergedMine: search(query: $mergedMine, type: ISSUE, first: 50) { nodes { ...prm } }
}
fragment prm on PullRequest {
  number title url updatedAt additions deletions headRefName headRefOid
  merged mergedAt
  mergeCommit { oid statusCheckRollup { state } }
  author { login }
  repository { nameWithOwner }
}
fragment pr on PullRequest {
  number title url isDraft updatedAt additions deletions
  mergeable mergeStateStatus reviewDecision totalCommentsCount headRefName headRefOid
  merged mergedAt
  mergeCommit { oid statusCheckRollup { state } }
  author { login }
  repository { nameWithOwner }
  reviewRequests { totalCount }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

type searchNodes struct {
	Nodes []prNode `json:"nodes"`
}

type prNode struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	URL                string    `json:"url"`
	IsDraft            bool      `json:"isDraft"`
	UpdatedAt          time.Time `json:"updatedAt"`
	Additions          int       `json:"additions"`
	Deletions          int       `json:"deletions"`
	Mergeable          string    `json:"mergeable"`
	MergeStateStatus   string    `json:"mergeStateStatus"`
	ReviewDecision     string    `json:"reviewDecision"`
	TotalCommentsCount int       `json:"totalCommentsCount"`
	HeadRefName        string    `json:"headRefName"`
	HeadRefOid         string    `json:"headRefOid"`
	Merged             bool      `json:"merged"`
	MergedAt           time.Time `json:"mergedAt"`
	MergeCommit        *struct {
		Oid               string `json:"oid"`
		StatusCheckRollup *struct {
			State string `json:"state"`
		} `json:"statusCheckRollup"`
	} `json:"mergeCommit"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReviewRequests struct {
		TotalCount int `json:"totalCount"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n prNode) ciState() string {
	if len(n.Commits.Nodes) == 0 {
		return ""
	}
	r := n.Commits.Nodes[0].Commit.StatusCheckRollup
	if r == nil {
		return ""
	}
	return r.State
}

func toPRItem(n prNode, reviewRequested bool) data.Item {
	it := data.Item{
		Kind:            data.KindPullRequest,
		Key:             fmt.Sprintf("%s#%d", n.Repository.NameWithOwner, n.Number),
		Title:           n.Title,
		Status:          "open",
		StatusCategory:  "new",
		URL:             n.URL,
		Repo:            n.Repository.NameWithOwner,
		Author:          n.Author.Login,
		Draft:           n.IsDraft,
		ReviewRequested: reviewRequested,
		Additions:       n.Additions,
		Deletions:       n.Deletions,
		Updated:         n.UpdatedAt,
		CIState:         n.ciState(),
		Mergeable:       n.Mergeable,
		MergeState:      n.MergeStateStatus,
		Branch:          n.HeadRefName,
		ReviewDecision:  n.ReviewDecision,
		HeadSHA:         n.HeadRefOid,
		Merged:          n.Merged,
		MergedAt:        n.MergedAt,
	}
	if n.MergeCommit != nil {
		it.MergeSHA = n.MergeCommit.Oid
		if n.MergeCommit.StatusCheckRollup != nil {
			it.MergeCIState = n.MergeCommit.StatusCheckRollup.State
		}
	}
	if it.Draft {
		it.Status = "draft"
	}
	it.Bucket = classify(n, reviewRequested)
	return it
}

func classify(n prNode, reviewRequested bool) data.Bucket {
	switch {
	case reviewRequested:
		return data.BucketNeedsMyReview
	case n.IsDraft:
		return data.BucketDraft
	case n.Mergeable == "CONFLICTING":
		return data.BucketResolveConflicts
	case n.ciState() == "FAILURE" || n.ciState() == "ERROR":
		return data.BucketFailingCI
	case n.ReviewDecision == "APPROVED":
		return data.BucketReadyToMerge
	case n.ReviewDecision == "CHANGES_REQUESTED",
		n.ReviewDecision == "REVIEW_REQUIRED" && n.TotalCommentsCount > 0:
		return data.BucketReviewerCommented
	case n.ReviewRequests.TotalCount > 0:
		return data.BucketWaitingForReview
	case n.ReviewRequests.TotalCount == 0:
		return data.BucketUnassignedReviewers
	default:
		return data.BucketOther
	}
}

const prDetailQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      body headRefName
      commits(last: 30) {
        nodes { commit { abbreviatedOid messageHeadline committedDate author { name } } }
      }
      files(first: 100) { nodes { path additions deletions } }
      comments(last: 20) { nodes { author { login } body createdAt } }
      checks: commits(last: 1) {
        nodes { commit { statusCheckRollup { contexts(first: 50) { nodes {
          __typename
          ... on CheckRun { name status conclusion }
          ... on StatusContext { context state }
        } } } } }
      }
    }
  }
}`

// FetchPRDetail returns body, commits, and changed files for one PR.
func FetchPRDetail(ctx context.Context, g *config.GitHub, repo string, number int) (*data.PRDetail, error) {
	token, err := g.ResolveToken()
	if err != nil {
		return nil, err
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("bad repo %q", repo)
	}
	var resp struct {
		Repository struct {
			PullRequest struct {
				Body        string `json:"body"`
				HeadRefName string `json:"headRefName"`
				Commits     struct {
					Nodes []struct {
						Commit struct {
							AbbreviatedOid  string    `json:"abbreviatedOid"`
							MessageHeadline string    `json:"messageHeadline"`
							CommittedDate   time.Time `json:"committedDate"`
							Author          struct {
								Name string `json:"name"`
							} `json:"author"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
				Files struct {
					Nodes []struct {
						Path      string `json:"path"`
						Additions int    `json:"additions"`
						Deletions int    `json:"deletions"`
					} `json:"nodes"`
				} `json:"files"`
				Comments struct {
					Nodes []struct {
						Author struct {
							Login string `json:"login"`
						} `json:"author"`
						Body      string    `json:"body"`
						CreatedAt time.Time `json:"createdAt"`
					} `json:"nodes"`
				} `json:"comments"`
				Checks struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								Contexts struct {
									Nodes []struct {
										Typename   string `json:"__typename"`
										Name       string `json:"name"`
										Status     string `json:"status"`
										Conclusion string `json:"conclusion"`
										Context    string `json:"context"`
										State      string `json:"state"`
									} `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"checks"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := gql(ctx, g, token, prDetailQuery, vars, &resp); err != nil {
		return nil, err
	}
	pr := resp.Repository.PullRequest
	d := &data.PRDetail{Body: jirafmt.Markdown(pr.Body), Branch: pr.HeadRefName}
	for _, n := range pr.Commits.Nodes {
		d.Commits = append(d.Commits, data.Commit{
			SHA:     n.Commit.AbbreviatedOid,
			Message: n.Commit.MessageHeadline,
			Author:  n.Commit.Author.Name,
			Date:    n.Commit.CommittedDate,
		})
	}
	for _, n := range pr.Files.Nodes {
		d.Files = append(d.Files, data.FileChange{Path: n.Path, Additions: n.Additions, Deletions: n.Deletions})
	}
	for _, n := range pr.Comments.Nodes {
		d.Comments = append(d.Comments, data.Comment{
			Author:  n.Author.Login,
			Body:    jirafmt.Markdown(n.Body),
			Created: n.CreatedAt,
		})
	}
	for _, cn := range pr.Checks.Nodes {
		if cn.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, c := range cn.Commit.StatusCheckRollup.Contexts.Nodes {
			cs := data.CheckStatus{Name: c.Name, State: c.Conclusion}
			if c.Typename == "StatusContext" {
				cs = data.CheckStatus{Name: c.Context, State: c.State}
			} else if cs.State == "" {
				cs.State = c.Status
			}
			d.Checks = append(d.Checks, cs)
		}
	}
	return d, nil
}

// Ping verifies credentials and returns the authenticated login.
func Ping(ctx context.Context, g *config.GitHub) (string, error) {
	token, err := g.ResolveToken()
	if err != nil {
		return "", err
	}
	return viewerLogin(ctx, g, token)
}

const checksQuery = `
query($owner: String!, $name: String!, $oid: GitObjectID!) {
  repository(owner: $owner, name: $name) {
    object(oid: $oid) {
      ... on Commit {
        checkSuites(first: 20) {
          nodes {
            app { name }
            status conclusion
            workflowRun { url createdAt workflow { name } }
            checkRuns(first: 30) {
              nodes { name status conclusion startedAt completedAt detailsUrl }
            }
          }
        }
      }
    }
  }
}`

// FetchChecks returns the workflow runs / check suites on one commit.
func FetchChecks(ctx context.Context, g *config.GitHub, repo, sha string) ([]data.WorkflowRun, error) {
	token, err := g.ResolveToken()
	if err != nil {
		return nil, err
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("bad repo %q", repo)
	}
	var resp struct {
		Repository struct {
			Object struct {
				CheckSuites struct {
					Nodes []struct {
						App struct {
							Name string `json:"name"`
						} `json:"app"`
						Status      string `json:"status"`
						Conclusion  string `json:"conclusion"`
						WorkflowRun *struct {
							URL       string    `json:"url"`
							CreatedAt time.Time `json:"createdAt"`
							Workflow  struct {
								Name string `json:"name"`
							} `json:"workflow"`
						} `json:"workflowRun"`
						CheckRuns struct {
							Nodes []struct {
								Name        string    `json:"name"`
								Status      string    `json:"status"`
								Conclusion  string    `json:"conclusion"`
								StartedAt   time.Time `json:"startedAt"`
								CompletedAt time.Time `json:"completedAt"`
								DetailsURL  string    `json:"detailsUrl"`
							} `json:"nodes"`
						} `json:"checkRuns"`
					} `json:"nodes"`
				} `json:"checkSuites"`
			} `json:"object"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "oid": sha}
	if err := gql(ctx, g, token, checksQuery, vars, &resp); err != nil {
		return nil, err
	}
	var runs []data.WorkflowRun
	for _, s := range resp.Repository.Object.CheckSuites.Nodes {
		r := data.WorkflowRun{
			Name:       s.App.Name,
			Status:     s.Status,
			Conclusion: s.Conclusion,
		}
		if s.WorkflowRun != nil {
			r.Name = s.WorkflowRun.Workflow.Name
			r.URL = s.WorkflowRun.URL
			r.Created = s.WorkflowRun.CreatedAt
		}
		for _, j := range s.CheckRuns.Nodes {
			if r.URL == "" && j.DetailsURL != "" {
				r.URL = j.DetailsURL
			}
			r.Jobs = append(r.Jobs, data.CheckRun{
				Name:       j.Name,
				Status:     j.Status,
				Conclusion: j.Conclusion,
				URL:        j.DetailsURL,
				Started:    j.StartedAt,
				Completed:  j.CompletedAt,
			})
		}
		// Skip placeholder suites: installed apps that never actually ran
		// anything on this commit (no workflow run, no jobs).
		if s.WorkflowRun == nil && len(r.Jobs) == 0 {
			continue
		}
		runs = append(runs, r)
	}
	return runs, nil
}

func viewerLogin(ctx context.Context, g *config.GitHub, token string) (string, error) {
	var resp struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := gql(ctx, g, token, `query { viewer { login } }`, nil, &resp); err != nil {
		return "", err
	}
	return resp.Viewer.Login, nil
}

func graphQLURL(g *config.GitHub) string {
	if g.Host == "" || g.Host == "github.com" {
		return "https://api.github.com/graphql"
	}
	return "https://" + g.Host + "/api/graphql"
}

func gql(ctx context.Context, g *config.GitHub, token, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// GitHub's GraphQL endpoint 502s transiently under load —
			// back off briefly and retry instead of failing the refresh.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", graphQLURL(g), bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("github graphql %s (transient — retried)", resp.Status)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("github graphql %s: %s", resp.Status, truncate(string(body)))
		}
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fmt.Errorf("github graphql: decoding response: %w", err)
		}
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("github graphql: %s", envelope.Errors[0].Message)
		}
		return json.Unmarshal(envelope.Data, out)
	}
	return lastErr
}

// ---- REST (issues) ----

type restSearchResponse struct {
	Items []struct {
		Number        int    `json:"number"`
		Title         string `json:"title"`
		HTMLURL       string `json:"html_url"`
		State         string `json:"state"`
		RepositoryURL string `json:"repository_url"`
	} `json:"items"`
}

func restSearch(ctx context.Context, g *config.GitHub, token, query string) (*restSearchResponse, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("per_page", "50")
	req, err := http.NewRequestWithContext(ctx, "GET", g.APIBase()+"/search/issues?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github %s: %s", resp.Status, truncate(string(body)))
	}
	var sr restSearchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("github: decoding response: %w", err)
	}
	return &sr, nil
}

// scopeQualifiers returns one search-scope suffix per query to run. Multiple
// org:/repo: qualifiers in a single query AND together (matching nothing), so
// each org/repo becomes its own query.
func scopeQualifiers(g *config.GitHub) []string {
	var scopes []string
	for _, o := range g.Orgs {
		scopes = append(scopes, "org:"+o)
	}
	for _, r := range g.Repos {
		scopes = append(scopes, "repo:"+r)
	}
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	return scopes
}

func repoFromURL(repoURL string) string {
	// https://api.github.com/repos/owner/repo -> owner/repo
	if i := strings.Index(repoURL, "/repos/"); i >= 0 {
		return repoURL[i+len("/repos/"):]
	}
	return repoURL
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ") // one line, no raw HTML spew
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
