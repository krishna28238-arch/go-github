// Package github provides a minimal GitHub API client with consistent offset
// pagination, following the pagination structure of google/go-github.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/go-querystring/query"
)

const (
	defaultBaseURL = "https://api.github.com/"
	defaultUA      = "sharmiaalono/go-github"
)

// ListOptions specifies the optional parameters to various List methods that
// support pagination.
type ListOptions struct {
	// Page, when greater than zero, is the page number of the results to
	// fetch. The zero value means "first page"; the page parameter is then
	// omitted from the request and the server returns its first page.
	Page int `url:"page,omitempty"`

	// PerPage, when greater than zero, is the number of results to include
	// per page.
	//
	// When PerPage is 0 (the zero value), per_page is omitted from the
	// request and the GitHub API applies its own default page size for the
	// endpoint. That default is typically 30 items, but it varies between
	// endpoints and can change server-side, so callers must not assume a
	// fixed value.
	//
	// Keeping page sizes consistent across a paginated walk means picking
	// one mode and staying with it for every request:
	//
	//   - either set PerPage once before the first request and keep the same
	//     non-zero value on every follow-up request, updating only Page from
	//     Response.NextPage;
	//   - or leave PerPage at 0 for the whole walk and let the server default
	//     apply to every page.
	//
	// Mixing the two modes (an explicit value on one request and the zero
	// value on the next) makes consecutive pages come back with different
	// sizes and silently skips or repeats records. The page size actually
	// used by the server is echoed in the Link header URLs and exposed as
	// Response.PerPage after each call, so a caller that relied on the
	// server default can learn the effective page size instead of guessing.
	PerPage int `url:"per_page,omitempty"`
}

// Response is a GitHub API response. It embeds the standard http.Response and
// adds the pagination values parsed from the Link header.
type Response struct {
	*http.Response

	// NextPage is the page number for the next request, extracted from the
	// rel="next" relation of the Link header. It is 0 when the response has
	// no next page (or no Link header).
	NextPage int

	// PrevPage is the page number of the previous page, extracted from the
	// rel="prev" relation of the Link header. It is 0 when absent.
	PrevPage int

	// FirstPage is the page number of the first page, extracted from the
	// rel="first" relation of the Link header. It is 0 when absent.
	FirstPage int

	// LastPage is the page number of the last page, extracted from the
	// rel="last" relation of the Link header. It is 0 when absent.
	LastPage int

	// PerPage is the page size reported by the server in the Link header
	// URLs. GitHub echoes the effective per_page there, including when it
	// was defaulted server-side because the request omitted per_page
	// (ListOptions.PerPage == 0). It is 0 when no relation carries a
	// per_page parameter.
	//
	// A caller that started a walk with ListOptions.PerPage == 0 can read
	// this field to learn the active page size and keep it explicit for
	// subsequent requests, so the context of the active page size is never
	// lost when following pagination links.
	PerPage int
}

// newResponse wraps an http.Response and parses its Link header pagination
// values.
func newResponse(r *http.Response) *Response {
	response := &Response{Response: r}
	response.parsePagination()
	return response
}

// parsePagination parses the Link header of the response, if any, and fills
// in NextPage, PrevPage, FirstPage and LastPage from the page parameter of
// each relation, plus PerPage from the per_page parameter echoed by the
// server.
//
// The page-number fields are derived from the page parameter only. They are
// unaffected by the presence, absence or position of per_page in the relation
// URLs, so pagination works identically whether per_page was explicit in the
// original request, defaulted by the server, or missing entirely.
func (r *Response) parsePagination() {
	if r == nil || r.Response == nil {
		return
	}
	links, ok := r.Response.Header["Link"]
	if !ok || len(links) == 0 {
		return
	}
	for _, link := range links {
		for _, segment := range strings.Split(link, ",") {
			segments := strings.Split(strings.TrimSpace(segment), ";")
			// A relation needs at least the URL and one parameter.
			if len(segments) < 2 {
				continue
			}
			// The URL is enclosed in angle brackets.
			trimmed := strings.TrimSpace(segments[0])
			if !strings.HasPrefix(trimmed, "<") || !strings.HasSuffix(trimmed, ">") {
				continue
			}
			u, err := url.Parse(trimmed[1 : len(trimmed)-1])
			if err != nil {
				continue
			}
			q := u.Query()

			// GitHub echoes the effective page size in every relation URL.
			// Capture it once, so the active page size context survives even
			// when the request itself omitted per_page.
			if r.PerPage == 0 {
				if perPage, err := strconv.Atoi(q.Get("per_page")); err == nil {
					r.PerPage = perPage
				}
			}

			page, err := strconv.Atoi(q.Get("page"))
			if err != nil {
				// No usable page number in this relation; it may still have
				// contributed the per_page captured above.
				continue
			}
			for _, param := range segments[1:] {
				switch strings.TrimSpace(param) {
				case `rel="next"`:
					r.NextPage = page
				case `rel="prev"`:
					r.PrevPage = page
				case `rel="first"`:
					r.FirstPage = page
				case `rel="last"`:
					r.LastPage = page
				}
			}
		}
	}
}

// Client is a minimal GitHub API client.
type Client struct {
	client    *http.Client
	BaseURL   *url.URL
	UserAgent string

	// Issues communicates with the issue-related endpoints.
	Issues *IssuesService
}

// NewClient returns a new GitHub API client. If a nil httpClient is given,
// a default client is used.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	baseURL, _ := url.Parse(defaultBaseURL)
	c := &Client{client: httpClient, BaseURL: baseURL, UserAgent: defaultUA}
	c.Issues = &IssuesService{client: c}
	return c
}

// NewRequest builds an API request relative to the client's BaseURL.
func (c *Client) NewRequest(method, urlStr string, body any) (*http.Request, error) {
	u, err := c.BaseURL.Parse(urlStr)
	if err != nil {
		return nil, err
	}
	var buf io.ReadWriter
	if body != nil {
		buf = &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		if err := enc.Encode(body); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, u.String(), buf)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return req, nil
}

// Do sends the request with ctx, decodes the JSON response body into v (when
// non-nil), and returns the wrapped *Response with the Link header pagination
// values already parsed.
func (c *Client) Do(ctx context.Context, req *http.Request, v any) (*Response, error) {
	if ctx == nil {
		return nil, errors.New("context must be non-nil")
	}
	req = req.WithContext(ctx)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	response := newResponse(resp)
	if err := checkResponse(resp); err != nil {
		return response, err
	}
	if v != nil {
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
			return response, err
		}
	}
	return response, nil
}

// ErrorResponse reports an API request that returned an error status.
type ErrorResponse struct {
	Response *http.Response
	Message  string `json:"message"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("%v %v: %d %v",
		e.Response.Request.Method, e.Response.Request.URL,
		e.Response.StatusCode, e.Message)
}

func checkResponse(r *http.Response) error {
	if code := r.StatusCode; 200 <= code && code <= 299 {
		return nil
	}
	errorResponse := &ErrorResponse{Response: r}
	data, err := io.ReadAll(r.Body)
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, errorResponse)
	}
	return errorResponse
}

// addOptions appends the query parameters encoded from opts to the URL s and
// returns the result.
//
// opts is usually a *ListOptions or an options struct embedding it. Fields
// holding their zero value and tagged `url:",omitempty"` — such as
// ListOptions.Page and ListOptions.PerPage — are omitted, so a zero
// ListOptions adds no parameters at all and the server defaults apply.
//
// The query string is always rebuilt deterministically from the given struct
// (go-querystring Values plus url.Values.Encode, which sorts keys), so
// reusing the same options struct across successive page calls keeps every
// parameter that was set, omits the zero ones, and never leaves stale or
// duplicated parameters behind.
func addOptions(s string, opts any) (string, error) {
	if opts == nil {
		return s, nil
	}
	// A typed nil pointer reaching here through the interface (e.g. a nil
	// *ListOptions) also means "no options".
	if rv := reflect.ValueOf(opts); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return s, err
	}
	qs, err := query.Values(opts)
	if err != nil {
		return s, err
	}
	u.RawQuery = qs.Encode()
	return u.String(), nil
}
