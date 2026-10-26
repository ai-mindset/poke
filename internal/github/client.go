package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ai-mindset/poke/internal/model"
)

type Client struct {
	token   string
	BaseURL string
	HTTP    *http.Client
	now     func() time.Time
	wait    func(context.Context, time.Duration) error
}

func New(token string) *Client {
	return &Client{
		token: token, BaseURL: "https://api.github.com",
		HTTP: &http.Client{Timeout: 30 * time.Second}, now: time.Now, wait: wait,
	}
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) Account(ctx context.Context) (model.Account, error) {
	var user struct {
		Login  string `json:"login"`
		NodeID string `json:"node_id"`
	}
	if err := c.request(ctx, "/user", &user); err != nil {
		return model.Account{}, err
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return model.Account{}, err
	}
	return model.Account{Host: base.Host, Login: user.Login, NodeID: user.NodeID}, nil
}

type apiIssue struct {
	NodeID        string `json:"node_id"`
	Number        int64  `json:"number"`
	Title         string `json:"title"`
	URL           string `json:"html_url"`
	RepositoryURL string `json:"repository_url"`
	State         string `json:"state"`
	UpdatedAt     string `json:"updated_at"`
	Assignees     []struct {
		Login string `json:"login"`
	} `json:"assignees"`
}

func (c *Client) AssignedIssues(ctx context.Context, org string) (model.SyncResult, error) {
	result := model.SyncResult{Issues: []model.Issue{}, Complete: true}
	indices := map[string]int{}
	// Search requests stay serial to reduce secondary rate-limit pressure.
	for _, state := range []string{"open", "closed"} {
		query := "is:issue is:" + state + " assignee:@me sort:updated-desc"
		if org != "" {
			query += " org:" + org
		}
		issues, complete, err := c.search(ctx, query)
		if err != nil {
			return model.SyncResult{}, err
		}
		result.Complete = result.Complete && complete
		for _, issue := range issues {
			if index, ok := indices[issue.NodeID]; ok {
				result.Issues[index] = issue
			} else {
				indices[issue.NodeID] = len(result.Issues)
				result.Issues = append(result.Issues, issue)
			}
		}
	}
	return result, nil
}

func (c *Client) search(ctx context.Context, query string) ([]model.Issue, bool, error) {
	issues := []model.Issue{}
	complete := true
	total := 0
	for page := 1; page <= 10; page++ {
		params := url.Values{"q": {query}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var data struct {
			Total      int        `json:"total_count"`
			Incomplete bool       `json:"incomplete_results"`
			Items      []apiIssue `json:"items"`
		}
		if err := c.request(ctx, "/search/issues?"+params.Encode(), &data); err != nil {
			return nil, false, err
		}
		total = data.Total
		complete = complete && !data.Incomplete && total <= 1000
		for _, item := range data.Items {
			repository, err := url.Parse(item.RepositoryURL)
			if err != nil {
				return nil, false, err
			}
			assignees := make([]string, 0, len(item.Assignees))
			for _, assignee := range item.Assignees {
				assignees = append(assignees, assignee.Login)
			}
			issues = append(issues, model.Issue{
				NodeID: item.NodeID, Number: item.Number, Title: item.Title, URL: item.URL,
				Repository: strings.TrimPrefix(repository.Path, "/repos/"), State: item.State,
				UpdatedAt: item.UpdatedAt, Assignees: assignees,
			})
		}
		if len(issues) >= min(total, 1000) {
			break
		}
	}
	return issues, complete && len(issues) >= total, nil
}

func (c *Client) request(ctx context.Context, path string, target any) error {
	_, err := c.requestPage(ctx, path, target)
	return err
}

func (c *Client) requestPage(ctx context.Context, path string, target any) (http.Header, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("User-Agent", "poke")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GitHub request failed: %w", err)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			err := json.NewDecoder(response.Body).Decode(target)
			response.Body.Close()
			return response.Header, err
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		details := string(body)
		if details == "" {
			details = http.StatusText(response.StatusCode)
		}
		if c.token != "" {
			details = strings.ReplaceAll(details, c.token, "[REDACTED]")
		}
		delay, retry := retryDelay(response, string(body), c.now())
		if retry && attempt == 0 {
			if delay > time.Minute {
				return nil, fmt.Errorf("GitHub API error %d: rate limited; retry after %s (automatic wait is limited to 60s).", response.StatusCode, delay.Round(time.Second))
			}
			if err := c.wait(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("GitHub API error %d: %s", response.StatusCode, details)
	}
	return nil, fmt.Errorf("GitHub API request failed after retrying.")
}

func retryDelay(response *http.Response, body string, now time.Time) (time.Duration, bool) {
	var delay time.Duration
	retry := false
	if value := response.Header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
			// Anything above the wait budget must fail rather than overflow or retry early.
			if seconds >= float64(math.MaxInt64)/float64(time.Second) {
				return time.Duration(math.MaxInt64), true
			}
			delay, retry = time.Duration(math.Ceil(seconds*float64(time.Second))), true
		} else if date, err := http.ParseTime(value); err == nil {
			delay, retry = max(date.Sub(now), 0), true
		}
	}
	if response.Header.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			delay, retry = max(delay, time.Unix(reset, 0).Sub(now), 0), true
		} else if !retry {
			delay, retry = time.Minute, true
		}
	}
	if !retry && (response.StatusCode == 429 || (response.StatusCode == 403 && strings.Contains(strings.ToLower(body), "secondary rate limit"))) {
		return time.Minute, true
	}
	return delay, retry
}
