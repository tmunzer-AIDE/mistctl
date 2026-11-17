package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tmunzer/mistapi-go/mistapi/models"
)

func TestShellIsTheOnlyAllowedWrite(t *testing.T) {
	site, dev := "aaaaaaaa-0000-0000-0000-000000000001", "00000000-0000-0000-1000-5c5b35000002"
	cases := []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/api/v1/self", true},
		{"POST", "/api/v1/sites/" + site + "/devices/" + dev + "/shell", true},
		{"PUT", "/api/v1/sites/" + site + "/devices/" + dev + "/shell", false},
		{"DELETE", "/api/v1/sites/" + site + "/devices/" + dev + "/shell", false},
		{"POST", "/api/v1/sites/" + site + "/devices/" + dev + "/restart", false},
		{"POST", "/api/v1/sites/" + site + "/devices/" + dev + "/shell/x", false},
		{"POST", "/api/v1/sites/" + site + "/devices/../../orgs/" + dev + "/shell", false},
		{"POST", "/api/v1/orgs/" + site + "/devices/" + dev + "/shell", false},
	}
	for _, c := range cases {
		if got := allowed(c.method, c.path); got != c.ok {
			t.Errorf("allowed(%s %s) = %v, want %v", c.method, c.path, got, c.ok)
		}
	}
}

func TestShellSession(t *testing.T) {
	var gotMethod, gotBody string
	c, hits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"session":"s1","url":"wss://api-ws.mist.com/ssh/s1"}`)
	})
	u, err := c.ShellSession(context.Background(), uuid.New(), uuid.New(), "node1")
	if err != nil || u != "wss://api-ws.mist.com/ssh/s1" {
		t.Fatalf("ShellSession = %q, %v", u, err)
	}
	if gotMethod != "POST" || gotBody != `{"node":"node1"}` || *hits != 1 {
		t.Fatalf("unexpected request %s %s (%d hits)", gotMethod, gotBody, *hits)
	}
	// Other writes are still refused by the same client.
	_, err = Call(context.Background(), c, func(ctx context.Context) (models.ApiResponse[models.Admin], error) {
		return c.SDK.SelfAccount().UpdateSelf(ctx, &models.Admin{})
	})
	var nro ErrNotReadOnly
	if !errors.As(err, &nro) || *hits != 1 {
		t.Fatalf("expected PUT to be refused, got %v (%d hits)", err, *hits)
	}
}

func TestShellSessionRejectsBadURL(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"session":"s1","url":"https://evil.example.com/"}`)
	})
	if _, err := c.ShellSession(context.Background(), uuid.New(), uuid.New(), ""); err == nil {
		t.Fatal("expected an error for a non-websocket URL")
	}
}

func TestShellURLPolicy(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	for u, want := range map[string]bool{
		"wss://api-ws.mist.com/ssh/s1?jwt=x": true,
		"wss://API-WS.mist.com:443/ssh":      true,
		"wss://api.mist.com/ssh":             true,
		"ws://api-ws.mist.com/ssh":           false, // cleartext
		"https://api-ws.mist.com/ssh":        false,
		"wss://user:pw@api-ws.mist.com/ssh":  false,
		"wss://api-ws.mist.com:8443/ssh":     false,
		"wss://other.mist.com/ssh":           false, // same parent domain is not enough
		"wss://api-ws.gc1.mist.com/ssh":      false, // another cloud
		"wss://evil.example.com/ssh":         false,
		"wss://mist.com.evil.example/ssh":    false,
		"wss://evilmist.com/ssh":             false,
		"wss:///ssh":                         false,
	} {
		err := c.CheckShellURL(u)
		if (err == nil) != want {
			t.Errorf("CheckShellURL(%s) = %v, want ok=%v", u, err, want)
		}
		if got := c.ShellHeader(u).Get("Authorization") == "Token tok"; got != want {
			t.Errorf("ShellHeader(%s) sends token = %v, want %v", u, got, want)
		}
	}
}

func TestShellSessionRejectsCleartext(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"session":"s1","url":"ws://api-ws.mist.com/ssh/s1?secret=1"}`)
	})
	_, err := c.ShellSession(context.Background(), uuid.New(), uuid.New(), "")
	if err == nil || !strings.Contains(err.Error(), "wss") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected a wss policy error without the query, got %v", err)
	}
}

func TestShellHTTPClientRefusesRedirects(t *testing.T) {
	c := ShellHTTPClient()
	req, _ := http.NewRequest("GET", "https://api-ws.mist.com/ssh", nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Fatal("redirects must be refused")
	}
}
