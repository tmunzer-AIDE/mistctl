package client

import (
	"context"
	"encoding/json"
	"errors"
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
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") {
		return "", errors.New("the shell API returned an invalid websocket URL")
	}
	return s.URL, nil
}

// ShellHeader returns the headers to send when opening the shell websocket.
// The API token is only sent to hosts of the same Mist cloud as the API
// (e.g. api-ws.mist.com for api.mist.com), never to an arbitrary host.
func (c *Client) ShellHeader(wsURL string) http.Header {
	h := http.Header{}
	u, err := url.Parse(wsURL)
	if err != nil || c.token == "" {
		return h
	}
	if sameCloud(c.Cloud.Host, u.Hostname()) {
		h.Set("Authorization", "Token "+c.token)
	}
	return h
}

// sameCloud reports whether host belongs to the same domain as the API host
// (the API host minus its first label).
func sameCloud(apiHost, host string) bool {
	apiHost, host = strings.ToLower(apiHost), strings.ToLower(host)
	if host == apiHost {
		return true
	}
	i := strings.IndexByte(apiHost, '.')
	if i < 0 {
		return false
	}
	parent := apiHost[i+1:]
	if !strings.Contains(parent, ".") {
		return false
	}
	return strings.HasSuffix(host, "."+parent)
}
