package github

import (
	"context"
	"fmt"
	"time"
)

// Issue represents a GitHub issue on a repository.
type Issue struct {
	ID     *int64  `json:"id,omitempty"`
	Number *int    `json:"number,omitempty"`
	Title  *string `json:"title,omitempty"`
	State  *string `json:"state,omitempty"`
}

// GetNumber returns the Number field if it is non-nil, zero otherwise.
func (i *Issue) GetNumber() int {
	if i == nil || i.Number == nil {
		return 0
	}
	return *i.Number
}

// IssuesService communicates with the issue-related endpoints of the
// GitHub API.
type IssuesService struct {
	client *Client
}

// IssueListOptions specifies the optional parameters to the
// IssuesService.ListByRepo method.
//
// It embeds ListOptions, so this endpoint inherits exactly the same
// pagination semantics as every other list endpoint — including the
// ListOptions.PerPage == 0 "use the server default" behaviour — without any
// separate handling.
type IssueListOptions struct {
	// State filters issues based on their state. Possible values are: open,
	// closed, all. Default is "open".
	State string `url:"state,omitempty"`

	// Labels filter issues by label.
	Labels []string `url:"labels,omitempty,comma"`

	// Sort specifies how to sort issues. Possible values are: created,
	// updated, comments. Default is "created".
	Sort string `url:"sort,omitempty"`

	// Direction in which to sort issues. Possible values are: asc, desc.
	Direction string `url:"direction,omitempty"`

	// Since filters issues by time of last update.
	Since time.Time `url:"since,omitempty"`

	ListOptions `url:",omitempty"`
}

// ListByRepo lists the issues for the specified repository.
//
// Pagination follows the documented ListOptions semantics: when
// opts.ListOptions.PerPage is 0, per_page is omitted from the request and
// GitHub applies its default page size; walk pages by updating only
// opts.ListOptions.Page from Response.NextPage, keeping the same PerPage
// value (or the same zero value) for every request in the walk.
func (s *IssuesService) ListByRepo(ctx context.Context, owner, repo string, opts *IssueListOptions) ([]*Issue, *Response, error) {
	u := fmt.Sprintf("repos/%v/%v/issues", owner, repo)
	u, err := addOptions(u, opts)
	if err != nil {
		return nil, nil, err
	}

	req, err := s.client.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}

	var issues []*Issue
	resp, err := s.client.Do(ctx, req, &issues)
	if err != nil {
		return nil, resp, err
	}

	return issues, resp, nil
}
