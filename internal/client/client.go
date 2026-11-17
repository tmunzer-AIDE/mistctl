// Package client wraps mistapi-go for mistctl: it builds an authenticated,
// GET-only SDK client and returns raw JSON so output matches the API exactly.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/tmunzer/mistapi-go/mistapi"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cloud"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// BaseTransport is the underlying HTTP transport. Tests replace it to point
// the client at a fake API.
var BaseTransport http.RoundTripper = http.DefaultTransport

// Client is a read-only Mist API client.
type Client struct {
	SDK   mistapi.ClientInterface
	Cloud cloud.Cloud
	Debug bool
	// Calls counts HTTP requests sent during this invocation.
	Calls int

	token string
}

// New builds a client for the given cloud and token.
func New(c cloud.Cloud, token string, debug bool) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("no API token configured (use `mistctl config set-context --token` or $MIST_APITOKEN)")
	}
	token = strings.TrimPrefix(token, "Token ")
	cl := &Client{Cloud: c, Debug: debug, token: token}
	tr := &guardTransport{base: BaseTransport, debug: debug, counter: &cl.Calls}
	cfg := mistapi.CreateConfigurationFromEnvironment(
		mistapi.WithEnvironment(c.Env),
		mistapi.WithApiTokenCredentials(mistapi.NewApiTokenCredentials("Token "+token)),
		mistapi.WithHttpConfiguration(mistapi.NewHttpConfiguration(
			mistapi.WithTimeout(60),
			mistapi.WithTransport(tr),
			mistapi.WithRetryConfiguration(mistapi.NewRetryConfiguration(
				mistapi.WithMaxRetryAttempts(2),
				mistapi.WithRetryInterval(1),
				mistapi.WithBackoffFactor(2),
				mistapi.WithHttpStatusCodesToRetry([]int64{408, 500, 502, 503, 504}),
				mistapi.WithHttpMethodsToRetry([]string{"GET"}),
			)),
		)),
	)
	cl.SDK = mistapi.NewClient(cfg)
	return cl, nil
}

// Raw is an API response body plus metadata.
type Raw struct {
	Status int
	Header http.Header
	Body   json.RawMessage
}

// APIError is a non-2xx answer from the Mist API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case 401:
		return "unauthorized (401): the API token is invalid or expired"
	case 403:
		return "permission denied (403): " + e.Message
	case 404:
		return "not found (404): " + e.Message
	case 429:
		return "rate limited (429): the hourly API budget is exhausted, see `mistctl usage`"
	}
	return fmt.Sprintf("API error %d: %s", e.Status, e.Message)
}

// Call runs one SDK call and returns the raw JSON body. Typed decoding errors
// from the SDK are ignored when the API itself answered 2xx, so a schema drift
// between the spec and the live API never hides data from the user.
func Call[T any](ctx context.Context, c *Client, fn func(context.Context) (models.ApiResponse[T], error)) (Raw, error) {
	cctx, capt := withCapture(ctx)
	_, err := fn(cctx)
	var nro ErrNotReadOnly
	if errors.As(err, &nro) {
		return Raw{}, nro
	}
	if capt.status == 0 {
		if err == nil {
			err = errors.New("no response from API")
		}
		return Raw{}, err
	}
	if capt.status < 200 || capt.status > 299 {
		return Raw{}, &APIError{Status: capt.status, Message: errorMessage(capt.body)}
	}
	if err != nil && c.Debug {
		fmt.Fprintf(os.Stderr, "warning: SDK decode error ignored, using raw response: %v\n", err)
	}
	body := capt.body
	if len(body) == 0 {
		body = []byte("null")
	}
	return Raw{Status: capt.status, Header: capt.header, Body: body}, nil
}

// errorMessage extracts a readable message from an error body. It ends up on
// the terminal, so control characters are escaped.
func errorMessage(b []byte) string {
	return output.Safe(rawErrorMessage(b))
}

func rawErrorMessage(b []byte) string {
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		for _, k := range []string{"detail", "message", "error", "reason"} {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	if s == "" {
		s = "(empty body)"
	}
	return s
}

// PageSize is the page size used for list endpoints (Mist maximum).
const PageSize = 1000

// maxPages bounds every pagination loop, as a last defence against an API
// that never signals the end.
const maxPages = 10000

// ListAll follows limit/page pagination of endpoints that return a JSON
// array, until all items (or max items when max > 0) are collected.
//
// The end is detected from X-Page-Total when the API sends it; otherwise a
// page shorter than the server's effective page size (X-Page-Limit, or the
// requested limit) ends the listing. An empty page always ends it.
func ListAll(ctx context.Context, max int, fetch func(ctx context.Context, limit, page int) (Raw, error)) ([]json.RawMessage, error) {
	var out []json.RawMessage
	limit := PageSize
	if max > 0 && max < limit {
		limit = max
	}
	for page := 1; page <= maxPages; page++ {
		r, err := fetch(ctx, limit, page)
		if err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(r.Body, &items); err != nil {
			return nil, fmt.Errorf("unexpected list response (expected a JSON array): %w", err)
		}
		out = append(out, items...)
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		if len(items) == 0 {
			return out, nil
		}
		if total := atoi(r.Header.Get("X-Page-Total")); total > 0 {
			if len(out) >= total {
				return out, nil
			}
			continue
		}
		effective := limit
		if l := atoi(r.Header.Get("X-Page-Limit")); l > 0 && l < effective {
			effective = l
		}
		if len(items) < effective {
			return out, nil
		}
	}
	return nil, fmt.Errorf("pagination did not end after %d pages", maxPages)
}

// PagedAll follows limit/page pagination of endpoints that wrap their items
// in {"results": [...], "limit": N, "total": N}. "total" is trusted over the
// page length; without it, a page shorter than the effective limit (the
// response's "limit", or the requested one) ends the listing.
func PagedAll(ctx context.Context, max int, fetch func(ctx context.Context, limit, page int) (Raw, error)) ([]json.RawMessage, error) {
	var out []json.RawMessage
	limit := PageSize
	if max > 0 && max < limit {
		limit = max
	}
	for page := 1; page <= maxPages; page++ {
		r, err := fetch(ctx, limit, page)
		if err != nil {
			return nil, err
		}
		var body struct {
			Results *[]json.RawMessage `json:"results"`
			Total   int                `json:"total"`
			Limit   int                `json:"limit"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Results == nil {
			return nil, fmt.Errorf("unexpected response: expected an object with a \"results\" list")
		}
		items := *body.Results
		out = append(out, items...)
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		if len(items) == 0 {
			return out, nil
		}
		if body.Total > 0 {
			if len(out) >= body.Total {
				return out, nil
			}
			continue
		}
		effective := limit
		if body.Limit > 0 && body.Limit < effective {
			effective = body.Limit
		}
		if len(items) < effective {
			return out, nil
		}
	}
	return nil, fmt.Errorf("pagination did not end after %d pages", maxPages)
}

// SearchAll follows the search_after cursor of Mist search endpoints.
func SearchAll(ctx context.Context, max int, fetch func(ctx context.Context, limit int, searchAfter *string) (Raw, error)) ([]json.RawMessage, error) {
	return SearchFiltered(ctx, max, nil, fetch)
}

// SearchFiltered is SearchAll with a local predicate: max applies to the
// items keep accepts, and pages are fetched until max matches are found or
// the results end. A repeated cursor or a malformed envelope is an error.
func SearchFiltered(ctx context.Context, max int, keep func(json.RawMessage) bool, fetch func(ctx context.Context, limit int, searchAfter *string) (Raw, error)) ([]json.RawMessage, error) {
	var out []json.RawMessage
	limit := PageSize
	if keep == nil && max > 0 && max < limit {
		limit = max
	}
	var after *string
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		r, err := fetch(ctx, limit, after)
		if err != nil {
			return nil, err
		}
		var body struct {
			Results *[]json.RawMessage `json:"results"`
			Next    string             `json:"next"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Results == nil {
			return nil, fmt.Errorf("unexpected search response: expected an object with a \"results\" list")
		}
		for _, it := range *body.Results {
			if keep == nil || keep(it) {
				out = append(out, it)
			}
		}
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		next := searchAfterFromNext(body.Next)
		if next == "" || len(*body.Results) == 0 {
			return out, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("the API returned the same search cursor twice (%s); stopping to avoid a loop", next)
		}
		seen[next] = true
		after = &next
	}
	return nil, fmt.Errorf("pagination did not end after %d pages", maxPages)
}

func searchAfterFromNext(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return u.Query().Get("search_after")
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ParseUUID parses s as a UUID.
func ParseUUID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(s)
	return u, err == nil
}

// Ptr returns a pointer to v, or nil for the zero value.
func Ptr[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}
