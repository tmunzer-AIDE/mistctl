package shell

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEscaper(t *testing.T) {
	cases := []struct {
		in   []string
		out  string
		quit bool
	}{
		{[]string{"show version\r"}, "show version\r", false},
		{[]string{"~."}, "", true},
		{[]string{"ls\r~."}, "ls\r", true},
		{[]string{"a~.b"}, "a~.b", false},
		{[]string{"~~."}, "~.", false},
		{[]string{"~x"}, "~x", false},
		{[]string{"\r~", "."}, "\r", true},
	}
	for _, c := range cases {
		e := escaper{enabled: true, lineStart: true}
		var got []byte
		quit := false
		for _, chunk := range c.in {
			o, q := e.feed([]byte(chunk))
			got = append(got, o...)
			if q {
				quit = true
				break
			}
		}
		if string(got) != c.out || quit != c.quit {
			t.Errorf("feed(%q) = %q, %v; want %q, %v", c.in, got, quit, c.out, c.quit)
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeDevice records what the client sends and answers like a Junos shell.
func fakeDevice(t *testing.T, got chan<- string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token tok" {
			http.Error(w, "no auth", 401)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		// first frame: resize
		typ, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		got <- typ.String() + " " + string(b)
		_ = c.Write(ctx, websocket.MessageBinary, []byte("user@sw-core> "))
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			got <- typ.String() + " " + string(b)
			if bytes.Equal(b, []byte("\x00exit\r")) {
				_ = c.Write(ctx, websocket.MessageBinary, []byte("bye\r\n"))
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunFramingAndDeviceClose(t *testing.T) {
	got := make(chan string, 20)
	srv := fakeDevice(t, got)
	inR, inW := io.Pipe()
	var out syncBuf
	var mu sync.Mutex
	cols, rows := 120, 40
	size := func() (int, int, bool) { mu.Lock(); defer mu.Unlock(); return cols, rows, true }

	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), Options{
			URL:        "ws" + strings.TrimPrefix(srv.URL, "http"),
			Header:     http.Header{"Authorization": {"Token tok"}},
			Stdin:      inR,
			Stdout:     &out,
			Size:       size,
			ResizePoll: 10 * time.Millisecond,
			Escape:     true,
		})
	}()

	want := func(s string) {
		t.Helper()
		select {
		case g := <-got:
			if g != s {
				t.Fatalf("device got %q, want %q", g, s)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %q", s)
		}
	}
	want(`MessageText {"resize":{"height":40,"width":120}}`)
	mu.Lock()
	cols, rows = 100, 30
	mu.Unlock()
	want(`MessageText {"resize":{"height":30,"width":100}}`)
	go inW.Write([]byte("show version\r"))
	want("MessageBinary \x00show version\r")
	go inW.Write([]byte("exit\r"))
	want("MessageBinary \x00exit\r")
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on device close", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the device closed")
	}
	if o := out.String(); !strings.Contains(o, "user@sw-core> ") || !strings.Contains(o, "bye") {
		t.Fatalf("stdout = %q", o)
	}
}

func TestRunEscape(t *testing.T) {
	got := make(chan string, 20)
	srv := fakeDevice(t, got)
	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), Options{
			URL:    "ws" + strings.TrimPrefix(srv.URL, "http"),
			Header: http.Header{"Authorization": {"Token tok"}},
			Stdin:  strings.NewReader("~."),
			Stdout: io.Discard,
			Escape: true,
		})
	}()
	select {
	case err := <-errc:
		if err != ErrEscape {
			t.Fatalf("Run = %v, want ErrEscape", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("escape did not end the session")
	}
}

func TestRunHandshakeError(t *testing.T) {
	got := make(chan string, 1)
	srv := fakeDevice(t, got)
	err := Run(context.Background(), Options{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Stdin: strings.NewReader(""), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Run = %v, want a 401 handshake error", err)
	}
}
