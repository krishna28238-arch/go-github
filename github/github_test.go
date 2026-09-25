package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

func TestParsePagination(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		next    int
		prev    int
		first   int
		last    int
		perPage int
	}{
		{
			name: "all four relations with per_page echoed in every URL",
			link: `<https://api.github.com/repositories/1/issues?per_page=50&page=1>; rel="prev", ` +
				`<https://api.github.com/repositories/1/issues?per_page=50&page=2>; rel="next", ` +
				`<https://api.github.com/repositories/1/issues?per_page=50&page=1>; rel="first", ` +
				`<https://api.github.com/repositories/1/issues?per_page=50&page=3>; rel="last"`,
			prev: 1, next: 2, first: 1, last: 3, perPage: 50,
		},
		{
			name: "parameter order varies between links",
			link: `<https://api.github.com/repositories/1/issues?page=2&per_page=25>; rel="next", ` +
				`<https://api.github.com/repositories/1/issues?per_page=25&page=1>; rel="first"`,
			next: 2, first: 1, perPage: 25,
		},
		{
			name: "no per_page in any link URL",
			link: `<https://api.github.com/repositories/1/issues?page=2>; rel="next", ` +
				`<https://api.github.com/repositories/1/issues?page=1>; rel="first"`,
			next: 2, first: 1, perPage: 0,
		},
		{
			name:    "server-defaulted page size of 30 is echoed",
			link:    `<https://api.github.com/repositories/1/issues?per_page=30&page=2>; rel="next"`,
			next:    2,
			perPage: 30,
		},
		{
			name:    "relation without a page number still contributes per_page",
			link:    `<https://api.github.com/repositories/1/issues?per_page=50>; rel="next"`,
			next:    0,
			perPage: 50,
		},
		{
			name: "malformed relations do not break parsing",
			link: `not-a-link; rel="next", ` +
				`<https://api.github.com/repositories/1/issues?page=abc>; rel="next", ` +
				`<https://api.github.com/repositories/1/issues?page=4>; rel="last"`,
			next: 0, last: 4, perPage: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Link", tt.link)
			resp := newResponse(&http.Response{Header: h})
			if resp.NextPage != tt.next {
				t.Errorf("NextPage = %d, want %d", resp.NextPage, tt.next)
			}
			if resp.PrevPage != tt.prev {
				t.Errorf("PrevPage = %d, want %d", resp.PrevPage, tt.prev)
			}
			if resp.FirstPage != tt.first {
				t.Errorf("FirstPage = %d, want %d", resp.FirstPage, tt.first)
			}
			if resp.LastPage != tt.last {
				t.Errorf("LastPage = %d, want %d", resp.LastPage, tt.last)
			}
			if resp.PerPage != tt.perPage {
				t.Errorf("PerPage = %d, want %d", resp.PerPage, tt.perPage)
			}
		})
	}
}

func TestParsePagination_NoLinkHeader(t *testing.T) {
	resp := newResponse(&http.Response{Header: http.Header{}})
	if resp.NextPage != 0 || resp.PrevPage != 0 || resp.FirstPage != 0 || resp.LastPage != 0 || resp.PerPage != 0 {
		t.Errorf("expected all pagination fields to be zero without a Link header, got %+v", resp)
	}
}

func TestAddOptions_ListOptions(t *testing.T) {
	tests := []struct {
		name string
		in   string
		opts *ListOptions
		want string
	}{
		{
			name: "nil options leave the URL untouched",
			in:   "issues",
			opts: nil,
			want: "issues",
		},
		{
			name: "zero ListOptions adds no parameters",
			in:   "issues",
			opts: &ListOptions{},
			want: "issues",
		},
		{
			name: "zero PerPage is omitted, Page is kept",
			in:   "issues",
			opts: &ListOptions{Page: 1, PerPage: 0},
			want: "issues?page=1",
		},
		{
			name: "explicit Page and PerPage",
			in:   "issues",
			opts: &ListOptions{Page: 2, PerPage: 50},
			want: "issues?page=2&per_page=50",
		},
		{
			name: "PerPage only",
			in:   "issues",
			opts: &ListOptions{PerPage: 100},
			want: "issues?per_page=100",
		},
		{
			name: "stale query is replaced, not merged",
			in:   "issues?page=7&per_page=99",
			opts: &ListOptions{Page: 2, PerPage: 0},
			want: "issues?page=2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := addOptions(tt.in, tt.opts)
			if err != nil {
				t.Fatalf("addOptions(%q, %+v) error: %v", tt.in, tt.opts, err)
			}
			if got != tt.want {
				t.Errorf("addOptions(%q, %+v) = %q, want %q", tt.in, tt.opts, got, tt.want)
			}
		})
	}
}

func TestAddOptions_TypedNilOptions(t *testing.T) {
	got, err := addOptions("issues", (*ListOptions)(nil))
	if err != nil {
		t.Fatalf("addOptions with typed nil error: %v", err)
	}
	if got != "issues" {
		t.Errorf("addOptions(typed nil) = %q, want %q", got, "issues")
	}
}

func TestAddOptions_IssueListOptions(t *testing.T) {
	// IssueListOptions embeds ListOptions; the embedded fields flatten into
	// the same query string, and a zero PerPage stays omitted here too.
	opts := &IssueListOptions{
		State:       "open",
		Labels:      []string{"bug", "help wanted"},
		Sort:        "created",
		Direction:   "desc",
		ListOptions: ListOptions{Page: 2, PerPage: 0},
	}
	got, err := addOptions("repos/o/r/issues", opts)
	if err != nil {
		t.Fatalf("addOptions error: %v", err)
	}
	want := "repos/o/r/issues?direction=desc&labels=bug%2Chelp+wanted&page=2&sort=created&state=open"
	if got != want {
		t.Errorf("zero PerPage:\n got %q\nwant %q", got, want)
	}

	opts.ListOptions = ListOptions{Page: 2, PerPage: 50}
	got, err = addOptions("repos/o/r/issues", opts)
	if err != nil {
		t.Fatalf("addOptions error: %v", err)
	}
	want = "repos/o/r/issues?direction=desc&labels=bug%2Chelp+wanted&page=2&per_page=50&sort=created&state=open"
	if got != want {
		t.Errorf("explicit PerPage:\n got %q\nwant %q", got, want)
	}
}

// mockIssuesServer serves two pages of two issues each for
// GET /repos/o/r/issues, echoing per_page=2 in every Link relation exactly
// like the real GitHub API - including when the request omitted per_page and
// the page size was defaulted server-side.
func mockIssuesServer(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*requests = append(*requests, r.URL.RawQuery)
		mu.Unlock()

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeIssues := func(from, to int) {
			w.Header().Set("Content-Type", "application/json")
			issues := make([]*Issue, 0, to-from+1)
			for n := from; n <= to; n++ {
				number := n
				issues = append(issues, &Issue{Number: &number})
			}
			_ = json.NewEncoder(w).Encode(issues)
		}

		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(
				`<http://%s/repos/o/r/issues?page=2&per_page=2>; rel="next", `+
					`<http://%s/repos/o/r/issues?page=1&per_page=2>; rel="first", `+
					`<http://%s/repos/o/r/issues?page=2&per_page=2>; rel="last"`,
				r.Host, r.Host, r.Host))
			writeIssues(1, 2)
		case "2":
			// Last page: no Link header, no next page.
			writeIssues(3, 4)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(mux)
}

func TestIssuesService_ListByRepo_PaginationFlow(t *testing.T) {
	var requests []string
	server := mockIssuesServer(t, &requests)
	defer server.Close()

	client := NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	ctx := context.Background()

	walk := func(t *testing.T, opts *IssueListOptions) []int {
		t.Helper()
		var got []int
		for pages := 0; ; pages++ {
			if pages >= 10 {
				t.Fatal("pagination walk did not terminate")
			}
			issues, resp, err := client.Issues.ListByRepo(ctx, "o", "r", opts)
			if err != nil {
				t.Fatalf("ListByRepo: %v", err)
			}
			for _, is := range issues {
				got = append(got, is.GetNumber())
			}
			if resp.NextPage != 0 && resp.PerPage != 2 {
				t.Errorf("page %d: resp.PerPage = %d, want 2 (echoed from the Link header)", pages+1, resp.PerPage)
			}
			if resp.NextPage == 0 {
				return got
			}
			opts.Page = resp.NextPage
		}
	}

	t.Run("default page size: per_page stays omitted on every page", func(t *testing.T) {
		requests = nil
		got := walk(t, &IssueListOptions{}) // Page 0, PerPage 0
		if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
			t.Errorf("collected issue numbers = %v, want [1 2 3 4]", got)
		}
		// First request carries no query at all; the follow-up request
		// carries exactly page=2 - per_page never appears, so every page
		// uses the server's default page size.
		if want := []string{"", "page=2"}; !reflect.DeepEqual(requests, want) {
			t.Errorf("request queries = %q, want %q", requests, want)
		}
	})

	t.Run("explicit page size: per_page is kept when following NextPage", func(t *testing.T) {
		requests = nil
		got := walk(t, &IssueListOptions{ListOptions: ListOptions{PerPage: 2}})
		if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
			t.Errorf("collected issue numbers = %v, want [1 2 3 4]", got)
		}
		// PerPage was set once and only Page is updated from NextPage, so
		// per_page is retained on the follow-up request.
		if want := []string{"per_page=2", "page=2&per_page=2"}; !reflect.DeepEqual(requests, want) {
			t.Errorf("request queries = %q, want %q", requests, want)
		}
	})

	t.Run("server default learned from Response.PerPage can be pinned", func(t *testing.T) {
		requests = nil
		opts := &IssueListOptions{}
		_, resp, err := client.Issues.ListByRepo(ctx, "o", "r", opts)
		if err != nil {
			t.Fatalf("ListByRepo: %v", err)
		}
		// The request omitted per_page; the server's effective page size is
		// reported back and can be pinned before following NextPage.
		if resp.PerPage != 2 {
			t.Fatalf("resp.PerPage = %d, want 2", resp.PerPage)
		}
		opts.PerPage = resp.PerPage
		opts.Page = resp.NextPage
		if _, _, err := client.Issues.ListByRepo(ctx, "o", "r", opts); err != nil {
			t.Fatalf("ListByRepo: %v", err)
		}
		if want := []string{"", "page=2&per_page=2"}; !reflect.DeepEqual(requests, want) {
			t.Errorf("request queries = %q, want %q", requests, want)
		}
	})
}
