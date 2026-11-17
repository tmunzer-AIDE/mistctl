package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cloud"
)

type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	old := BaseTransport
	BaseTransport = redirect{u}
	t.Cleanup(func() { BaseTransport = old })
	c, err := New(cloud.All[0], "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	return c, &hits
}

func TestWritesAreRefused(t *testing.T) {
	c, hits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{}") })
	_, err := Call(context.Background(), c, func(ctx context.Context) (models.ApiResponse[models.Admin], error) {
		return c.SDK.SelfAccount().UpdateSelf(ctx, &models.Admin{})
	})
	var nro ErrNotReadOnly
	if !errors.As(err, &nro) || nro.Method != "PUT" {
		t.Fatalf("expected ErrNotReadOnly for PUT, got %v", err)
	}
	if *hits != 0 {
		t.Fatalf("write reached the server (%d hits)", *hits)
	}
}

func TestDecodeErrorsFallBackToRawBody(t *testing.T) {
	// "requests" should be an int; the SDK fails to decode but we keep the data.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"requests":"many","request_limit":5000,"new_field":true}`)
	})
	raw, err := Call(context.Background(), c, c.SDK.SelfAccount().GetSelfApiUsage)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw.Body) != `{"requests":"many","request_limit":5000,"new_field":true}` {
		t.Fatalf("raw body altered: %s", raw.Body)
	}
}

func TestAPIErrors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"detail":"You do not have permission"}`)
	})
	_, err := Call(context.Background(), c, c.SDK.SelfAccount().GetSelf)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Message != "You do not have permission" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListAllPaginates(t *testing.T) {
	pages := map[int][]int{1: {1, 2}, 2: {3, 4}, 3: {5}}
	calls := 0
	items, err := ListAll(context.Background(), 0, func(ctx context.Context, limit, page int) (Raw, error) {
		calls++
		b, _ := json.Marshal(pages[page])
		return Raw{Body: b, Header: http.Header{"X-Page-Total": {"5"}}}, nil
	})
	if err != nil || len(items) != 5 || calls != 3 {
		t.Fatalf("got %d items in %d calls, err=%v", len(items), calls, err)
	}
	// the page size must shrink to max, and max is respected
	items, _ = ListAll(context.Background(), 3, func(ctx context.Context, limit, page int) (Raw, error) {
		if limit != 3 {
			t.Errorf("limit = %d, want 3", limit)
		}
		return Raw{Body: []byte(`[1,2,3]`), Header: http.Header{}}, nil
	})
	if len(items) != 3 {
		t.Fatalf("max not respected: %d", len(items))
	}
}

func TestSearchAllFollowsCursor(t *testing.T) {
	var afters []string
	items, err := SearchAll(context.Background(), 0, func(ctx context.Context, limit int, after *string) (Raw, error) {
		if after == nil {
			return Raw{Body: []byte(`{"results":[1,2],"next":"/api/v1/x/search?limit=2&search_after=%5B%22a%22%5D"}`)}, nil
		}
		afters = append(afters, *after)
		return Raw{Body: []byte(`{"results":[3]}`)}, nil
	})
	if err != nil || len(items) != 3 || len(afters) != 1 || afters[0] != `["a"]` {
		t.Fatalf("items=%d afters=%v err=%v", len(items), afters, err)
	}
}

func TestEmptyToken(t *testing.T) {
	if _, err := New(cloud.All[0], "  ", false); err == nil {
		t.Fatal("expected error for empty token")
	}
}

// The server may cap a page below the requested limit: a trustworthy total
// must win over the short-page heuristic.
func TestPagedAllTrustsTotalOverShortPage(t *testing.T) {
	pages := map[int]string{1: `{"results":[1,2],"limit":2,"total":3}`, 2: `{"results":[3],"limit":2,"total":3}`}
	calls := 0
	items, err := PagedAll(context.Background(), 0, func(ctx context.Context, limit, page int) (Raw, error) {
		calls++
		return Raw{Body: []byte(pages[page])}, nil
	})
	if err != nil || len(items) != 3 || calls != 2 {
		t.Fatalf("items=%d calls=%d err=%v", len(items), calls, err)
	}
	// Without total, the effective "limit" of the response is used.
	pages = map[int]string{1: `{"results":[1,2],"limit":2}`, 2: `{"results":[3],"limit":2}`}
	calls = 0
	items, _ = PagedAll(context.Background(), 0, func(ctx context.Context, limit, page int) (Raw, error) {
		calls++
		return Raw{Body: []byte(pages[page])}, nil
	})
	if len(items) != 3 || calls != 2 {
		t.Fatalf("effective limit: items=%d calls=%d", len(items), calls)
	}
	if _, err := PagedAll(context.Background(), 0, func(ctx context.Context, limit, page int) (Raw, error) {
		return Raw{Body: []byte(`[1,2]`)}, nil
	}); err == nil {
		t.Error("malformed envelope should be an error")
	}
}

func TestListAllUsesEffectivePageLimit(t *testing.T) {
	calls := 0
	items, err := ListAll(context.Background(), 0, func(ctx context.Context, limit, page int) (Raw, error) {
		calls++
		if page == 1 {
			return Raw{Body: []byte(`[1,2]`), Header: http.Header{"X-Page-Limit": {"2"}}}, nil
		}
		return Raw{Body: []byte(`[3]`), Header: http.Header{"X-Page-Limit": {"2"}}}, nil
	})
	if err != nil || len(items) != 3 || calls != 2 {
		t.Fatalf("items=%d calls=%d err=%v", len(items), calls, err)
	}
}

func TestSearchAllRejectsRepeatedCursor(t *testing.T) {
	calls := 0
	_, err := SearchAll(context.Background(), 0, func(ctx context.Context, limit int, after *string) (Raw, error) {
		calls++
		if calls > 10 {
			t.Fatal("cursor loop not detected")
		}
		return Raw{Body: []byte(`{"results":[1],"next":"/x?search_after=%5B1%5D"}`)}, nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("expected an error after 2 calls, got %v after %d", err, calls)
	}
	if _, err := SearchAll(context.Background(), 0, func(ctx context.Context, limit int, after *string) (Raw, error) {
		return Raw{Body: []byte(`[]`)}, nil
	}); err == nil {
		t.Error("malformed envelope should be an error")
	}
}

// The output limit applies to matches, so a match on a later page is found.
func TestSearchFilteredLimitsMatches(t *testing.T) {
	pages := []string{
		`{"results":[{"type":"OTHER"}],"next":"/x?search_after=1"}`,
		`{"results":[{"type":"TARGET"}],"next":"/x?search_after=2"}`,
		`{"results":[{"type":"TARGET"}]}`,
	}
	calls := 0
	keep := func(r json.RawMessage) bool { return string(r) == `{"type":"TARGET"}` }
	items, err := SearchFiltered(context.Background(), 1, keep, func(ctx context.Context, limit int, after *string) (Raw, error) {
		calls++
		return Raw{Body: []byte(pages[calls-1])}, nil
	})
	if err != nil || len(items) != 1 || calls != 2 {
		t.Fatalf("items=%v calls=%d err=%v", items, calls, err)
	}
}

// Every hop of a redirect goes through the guard (and is counted).
func TestRedirectHopsGoThroughTheGuard(t *testing.T) {
	c, hits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/self" {
			http.Redirect(w, r, "/api/v1/self/moved", http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"email":"jane@example.com"}`)
	})
	raw, err := Call(context.Background(), c, c.SDK.SelfAccount().GetSelf)
	if err != nil {
		t.Fatal(err)
	}
	if *hits != 2 || c.Calls != 2 {
		t.Errorf("hits=%d guard calls=%d, want 2 and 2", *hits, c.Calls)
	}
	if string(raw.Body) != `{"email":"jane@example.com"}` {
		t.Errorf("body = %s", raw.Body)
	}
}

// Stray SDK environment variables must not change the requests.
func TestSDKEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("MISTAPI_ACCEPT", "text/plain")
	t.Setenv("MISTAPI_AUTHORIZATION", "Token from-env")
	var accept, auth string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		accept, auth = r.Header.Get("Accept"), r.Header.Get("Authorization")
		fmt.Fprint(w, `{}`)
	})
	if _, err := Call(context.Background(), c, c.SDK.SelfAccount().GetSelf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(accept, "text/plain") || auth != "Token tok" {
		t.Errorf("Accept=%q Authorization=%q", accept, auth)
	}
}
