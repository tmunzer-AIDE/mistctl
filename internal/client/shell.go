package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/tmunzer/mistapi-go/mistapi/models"
)

// ShellSession asks Mist for a remote shell on a switch or gateway and
// returns the websocket URL to connect to. node selects one member of an SRX
// HA cluster ("node0" or "node1") and may be empty.
//
// This is the only non-GET call mistctl makes; the transport guard allows
// exactly this endpoint and nothing else.
func (c *Client) ShellSession(ctx context.Context, site, device uuid.UUID, node string) (string, error) {
	body := &models.ShellNode{}
	if node != "" {
		n := models.HaClusterNodeEnum(node)
		body.Node = &n
	}
	raw, err := Call(ctx, c, func(ctx context.Context) (models.ApiResponse[models.WebsocketSessionWithUrl], error) {
		return c.SDK.UtilitiesCommon().CreateSiteDeviceShellSession(ctx, site, device, body)
	})
	if err != nil {
		return "", err
	}
	var s struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw.Body, &s); err != nil || s.URL == "" {
		return "", errors.New("the shell API did not return a websocket URL")
	}
	if err := c.CheckShellURL(s.URL); err != nil {
		return "", err
	}
	return s.URL, nil
}

// ShellHosts returns the websocket hosts allowed for the client's cloud: the
// API host and its "api-ws" counterpart (api.gc1.mist.com ->
// api-ws.gc1.mist.com). Nothing else under the parent domain is trusted.
func (c *Client) ShellHosts() []string {
	host := strings.ToLower(c.Cloud.Host)
	hosts := []string{host}
	if rest, ok := strings.CutPrefix(host, "api."); ok {
		hosts = append(hosts, "api-ws."+rest)
	}
	return hosts
}

// CheckShellURL enforces the websocket policy of remote shells: wss only, a
// host of the selected cloud (see ShellHosts), the default port (or 443) and
// no user info. The API token is only ever sent to such a URL.
func (c *Client) CheckShellURL(wsURL string) error {
	u, err := url.Parse(wsURL)
	if err != nil {
		return errors.New("the shell API returned an invalid websocket URL")
	}
	bad := func(why string) error {
		return fmt.Errorf("refusing the websocket URL returned by the shell API (%s): %s", redactURL(u), why)
	}
	switch {
	case u.Scheme != "wss":
		return bad("only wss:// (TLS) is allowed")
	case u.User != nil:
		return bad("user info is not allowed")
	case u.Hostname() == "":
		return bad("no host")
	case u.Port() != "" && u.Port() != "443":
		return bad("unexpected port " + u.Port())
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range c.ShellHosts() {
		if host == h {
			return nil
		}
	}
	return bad(fmt.Sprintf("host is not one of %s", strings.Join(c.ShellHosts(), ", ")))
}

// redactURL drops the query (which can carry a session secret) from errors.
func redactURL(u *url.URL) string {
	v := *u
	v.RawQuery, v.User = "", nil
	return v.String()
}

// ShellHeader returns the headers to send when opening the shell websocket.
// The API token is only added when the URL passes CheckShellURL.
func (c *Client) ShellHeader(wsURL string) http.Header {
	h := http.Header{}
	if c.token == "" || c.CheckShellURL(wsURL) != nil {
		return h
	}
	h.Set("Authorization", "Token "+c.token)
	return h
}

// ShellHTTPClient is the HTTP client for the websocket handshake. Redirects
// are refused, so the token can never follow one to another host.
func ShellHTTPClient() *http.Client {
	return &http.Client{
		Transport: BaseTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("refusing to follow a websocket redirect to %s", req.URL.Host)
		},
	}
}
