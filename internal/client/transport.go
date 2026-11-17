package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ErrNotReadOnly is returned when anything tries to send a non-GET request.
type ErrNotReadOnly struct{ Method, URL string }

func (e ErrNotReadOnly) Error() string {
	return fmt.Sprintf("mistctl is read-only: refusing %s %s", e.Method, e.URL)
}

type captureKey struct{}

// capture records the raw response of the last request made with a context.
type capture struct {
	status int
	header http.Header
	body   []byte
}

func withCapture(ctx context.Context) (context.Context, *capture) {
	c := &capture{}
	return context.WithValue(ctx, captureKey{}, c), c
}

// guardTransport enforces GET-only access and keeps a copy of every response
// body so mistctl can print exactly what the API returned.
type guardTransport struct {
	base    http.RoundTripper
	debug   bool
	counter *int
}

func (t *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, ErrNotReadOnly{req.Method, req.URL.Path}
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if t.counter != nil {
		*t.counter++
	}
	if t.debug {
		status := "ERR"
		if resp != nil {
			status = resp.Status
		}
		fmt.Fprintf(os.Stderr, "> %s %s -> %s (%s)\n", req.Method, req.URL.String(), status, time.Since(start).Round(time.Millisecond))
	}
	if err != nil || resp == nil {
		return resp, err
	}
	if c, ok := req.Context().Value(captureKey{}).(*capture); ok {
		b, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, rerr
		}
		c.status, c.header, c.body = resp.StatusCode, resp.Header.Clone(), b
		resp.Body = io.NopCloser(bytes.NewReader(b))
	}
	return resp, nil
}
