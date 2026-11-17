package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
