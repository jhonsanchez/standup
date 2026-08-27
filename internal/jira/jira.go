package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/jirafmt"
)

const defaultJQL = `assignee = currentUser() AND sprint in openSprints() ORDER BY updated DESC`

var fields = []string{"summary", "status", "issuetype", "priority", "subtasks", "description", "comment", "updated"}

type searchResponse struct {
	Issues []issue `json:"issues"`
	// Cloud (v3 /search/jql) pagination.
	NextPageToken string `json:"nextPageToken"`
	IsLast        bool   `json:"isLast"`
	// Data Center (v2 /search) pagination.
	StartAt    int `json:"startAt"`
	MaxResults int `json:"maxResults"`
	Total      int `json:"total"`
}

type issue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  status `json:"status"`
		Type    struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority *struct {
			Name string `json:"name"`
		} `json:"priority"`
		Subtasks    []issue         `json:"subtasks"`
		Description json.RawMessage `json:"description"`
		Comment     *struct {
			Total int `json:"total"`
		} `json:"comment"`
		Updated string `json:"updated"`
	} `json:"fields"`
}

type status struct {
	Name     string `json:"name"`
	Category struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

func buildJQL(j *config.Jira) string {
	if j.JQL != "" {
		return j.JQL
	}
	jql := defaultJQL
	if p := strings.TrimSpace(j.Projects); p != "" {
		keys := strings.Split(p, ",")
		for i := range keys {
			keys[i] = strings.TrimSpace(keys[i])
		}
		jql = fmt.Sprintf("project in (%s) AND %s", strings.Join(keys, ", "), jql)
	}
	return jql
}

// FetchSprintIssues returns the user's issues in open sprints, subtasks
// inlined. Supports Atlassian Cloud (basic auth, v3) and Jira Data Center
// (PAT bearer auth, v2).
func FetchSprintIssues(ctx context.Context, j *config.Jira) ([]data.Item, error) {
	token, err := j.ResolveToken()
	if err != nil {
		return nil, err
	}
	jql := buildJQL(j)
	base := strings.TrimRight(j.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("jira: base_url is empty (unset env var?)")
	}

	var items []data.Item
	pageToken := ""
	startAt := 0
	for {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("maxResults", "100")
		q.Set("fields", strings.Join(fields, ","))
		endpoint := base + "/rest/api/3/search/jql"
		if j.IsDataCenter() {
			endpoint = base + "/rest/api/2/search"
			q.Set("startAt", fmt.Sprint(startAt))
		} else if pageToken != "" {
			q.Set("nextPageToken", pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if j.IsDataCenter() {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.SetBasicAuth(j.Email, token)
		}
		req.Header.Set("Accept", "application/json")

		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			msg := strings.TrimSpace(string(body))
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return nil, fmt.Errorf("jira %s: %s", resp.Status, msg)
		}
		var sr searchResponse
		if err := json.Unmarshal(body, &sr); err != nil {
			return nil, fmt.Errorf("jira: decoding response: %w", err)
		}
		for _, is := range sr.Issues {
			items = append(items, toItem(base, is))
		}

		if j.IsDataCenter() {
			startAt += len(sr.Issues)
			if len(sr.Issues) == 0 || startAt >= sr.Total {
				break
			}
		} else {
			if sr.IsLast || sr.NextPageToken == "" {
				break
			}
			pageToken = sr.NextPageToken
		}
	}

	// Display grouping/ordering happens in the UI (status sections).
	return items, nil
}

func doGet(ctx context.Context, j *config.Jira, token, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	if j.IsDataCenter() {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.SetBasicAuth(j.Email, token)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("jira %s: %s", resp.Status, msg)
	}
	return body, nil
}

// Ping verifies credentials by fetching the authenticated user.
func Ping(ctx context.Context, j *config.Jira) error {
	token, err := j.ResolveToken()
	if err != nil {
		return err
	}
	base := strings.TrimRight(j.BaseURL, "/")
	if base == "" {
		return fmt.Errorf("base_url is empty (unset env var?)")
	}
	api := "/rest/api/3/myself"
	if j.IsDataCenter() {
		api = "/rest/api/2/myself"
	}
	_, err = doGet(ctx, j, token, base+api)
	return err
}

// FetchIssue returns one issue (used for subtask detail).
func FetchIssue(ctx context.Context, j *config.Jira, key string) (data.Item, error) {
	token, err := j.ResolveToken()
	if err != nil {
		return data.Item{}, err
	}
	base := strings.TrimRight(j.BaseURL, "/")
	api := "/rest/api/3/issue/"
	if j.IsDataCenter() {
		api = "/rest/api/2/issue/"
	}
	body, err := doGet(ctx, j, token, base+api+key+"?fields="+strings.Join(fields, ","))
	if err != nil {
		return data.Item{}, err
	}
	var is issue
	if err := json.Unmarshal(body, &is); err != nil {
		return data.Item{}, fmt.Errorf("jira: decoding issue: %w", err)
	}
	return toItem(base, is), nil
}

// FetchComments returns an issue's comments, newest last.
func FetchComments(ctx context.Context, j *config.Jira, key string) ([]data.Comment, error) {
	token, err := j.ResolveToken()
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(j.BaseURL, "/")
	api := "/rest/api/3/issue/"
	if j.IsDataCenter() {
		api = "/rest/api/2/issue/"
	}
	body, err := doGet(ctx, j, token, base+api+key+"/comment?maxResults=50")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Comments []struct {
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Body    json.RawMessage `json:"body"`
			Created string          `json:"created"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("jira: decoding comments: %w", err)
	}
	var out []data.Comment
	for _, c := range resp.Comments {
		out = append(out, data.Comment{
			Author:  c.Author.DisplayName,
			Body:    strings.TrimSpace(descriptionText(c.Body)),
			Created: parseJiraTime(c.Created),
		})
	}
	return out, nil
}

func parseJiraTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// descriptionText handles both API shapes: v2 returns a wiki-markup string,
// v3 returns an Atlassian Document Format tree. Both render to styled text.
func descriptionText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return jirafmt.Wiki(s)
	}
	return jirafmt.ADF(raw)
}

func toItem(base string, is issue) data.Item {
	it := data.Item{
		Kind:           data.KindJiraIssue,
		Key:            is.Key,
		Title:          is.Fields.Summary,
		Status:         is.Fields.Status.Name,
		StatusCategory: is.Fields.Status.Category.Key,
		IssueType:      is.Fields.Type.Name,
		URL:            base + "/browse/" + is.Key,
	}
	if is.Fields.Priority != nil {
		it.Priority = is.Fields.Priority.Name
	}
	it.Description = descriptionText(is.Fields.Description)
	if is.Fields.Comment != nil {
		it.CommentCount = is.Fields.Comment.Total
	}
	it.Updated = parseJiraTime(is.Fields.Updated)
	for _, st := range is.Fields.Subtasks {
		it.Subtasks = append(it.Subtasks, data.Subtask{
			Key:            st.Key,
			Summary:        st.Fields.Summary,
			Status:         st.Fields.Status.Name,
			StatusCategory: st.Fields.Status.Category.Key,
			URL:            base + "/browse/" + st.Key,
		})
	}
	return it
}
