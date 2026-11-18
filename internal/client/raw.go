package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Get sends one GET request to an API path the SDK has no working method
// for, and returns the raw JSON body. It goes through the same read-only
// guard, debug trace and call counter as the SDK calls. path starts with
// "/api/"; its segments must already be escaped.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (Raw, error) {
	u := url.URL{Scheme: "https", Host: c.Cloud.Host, Path: path, RawQuery: query.Encode()}
	if p, err := url.PathUnescape(path); err == nil && p != path {
		u.Path, u.RawPath = p, path
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Raw{}, err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	hc := &http.Client{
		Transport: &guardTransport{base: BaseTransport, debug: c.Debug, counter: &c.Calls},
		// The token must never follow a redirect to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Raw{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Raw{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Raw{}, &APIError{Status: resp.StatusCode, Message: errorMessage(body)}
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("null")
	}
	return Raw{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: body}, nil
}
