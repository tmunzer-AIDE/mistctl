package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func TestShellHeaderOnlyForSameCloud(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	for u, want := range map[string]bool{
		"wss://api-ws.mist.com/ssh":       true,
		"wss://api.mist.com/ssh":          true,
		"wss://evil.example.com/ssh":      false,
		"wss://mist.com.evil.example/ssh": false,
		"wss://evilmist.com/ssh":          false,
	} {
		if got := c.ShellHeader(u).Get("Authorization") == "Token tok"; got != want {
			t.Errorf("ShellHeader(%s) sends token = %v, want %v", u, got, want)
		}
	}
}
