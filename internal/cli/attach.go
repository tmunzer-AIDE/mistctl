package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
	"github.com/tmunzer-AIDE/mistctl/internal/shell"
	"github.com/tmunzer-AIDE/mistctl/internal/term"
)

// Test seams.
var (
	newTerminal           = func() term.Terminal { return term.OS{} }
	attachStdin io.Reader = os.Stdin
	attachOut   io.Writer = os.Stdout
)

func newAttachCmd(a *app) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "attach DEVICE",
		Short: "Open a remote shell on a switch or gateway",
		Long: `Open an interactive remote shell (Junos CLI) on a switch or gateway through
the Mist cloud, like kubectl attach. No direct reachability to the device is
needed.

DEVICE can be a name, an ID or a MAC address.

Read-only: when the org allows it, Mist gives read-only (Observer) admins a
read-only remote shell. With a token that has write privileges the shell is
read-write, and mistctl warns about it. Creating the shell session is the only
non-GET request mistctl ever sends.

The terminal runs in raw mode, so Ctrl+C goes to the device. Type "exit" on
the device, or "~." at the start of a line, to end the session.`,
		Example: `  mistctl attach sw-core
  mistctl attach 5c:5b:35:00:11:22
  mistctl attach srx-hq --node node1`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeAttach,
		RunE: func(cmd *cobra.Command, args []string) error {
			if node != "" && node != "node0" && node != "node1" {
				return fmt.Errorf("invalid --node %q (valid: node0, node1)", node)
			}
			return a.attach(cmd.Context(), args[0], node)
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "SRX HA cluster member to connect to: node0, node1")
	_ = cmd.RegisterFlagCompletionFunc("node", fixedCompletion("node0", "node1"))
	return cmd
}

func (a *app) completeAttach(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, c := range a.completeIndex(cmd, kindDevices, toComplete, true) {
		if !strings.Contains(c, "\tap ") {
			out = append(out, c)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func (a *app) attach(ctx context.Context, name, node string) error {
	e, err := a.lookup(ctx, kindDevices, name)
	if err != nil {
		return err
	}
	label := output.Safe(e.Name)
	if label == "" {
		label = output.Safe(name)
	}
	switch e.Type {
	case "switch", "gateway", "":
	case "ap":
		return fmt.Errorf("%s is an access point: remote shell is only available on switches and gateways", label)
	default:
		return fmt.Errorf("%s is a %s: remote shell is only available on switches and gateways", label, e.Type)
	}
	if e.SiteID == "" {
		return fmt.Errorf("device %q is not assigned to a site", label)
	}
	site, err := uuid.Parse(e.SiteID)
	if err != nil {
		return fmt.Errorf("device %q has an invalid site ID %q", label, e.SiteID)
	}
	dev, err := uuid.Parse(e.ID)
	if err != nil {
		return fmt.Errorf("device %q has an invalid ID %q", label, e.ID)
	}

	tty := newTerminal()
	if !tty.IsTerminal() {
		return errors.New("attach needs an interactive terminal (stdin and stdout must be a TTY)")
	}

	org, _ := a.orgID(ctx)
	if _, raw, err := a.self(ctx); err == nil {
		if roles := writableRoles(raw, org.String(), e.SiteID); len(roles) > 0 {
			warnWritableShell(a.err, roles)
		}
	} else {
		fmt.Fprintf(a.err, "warning: could not check the token privileges: %v\n", err)
	}

	cl, err := a.client()
	if err != nil {
		return err
	}
	wsURL, err := cl.ShellSession(ctx, site, dev, node)
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			return fmt.Errorf("%w\nhint: read-only (Observer) admins need the org setting that allows them a read-only remote shell", err)
		}
		return fmt.Errorf("could not create the shell session: %w", err)
	}

	restore, err := tty.MakeRaw()
	if err != nil {
		return fmt.Errorf("could not set the terminal in raw mode: %w", err)
	}
	fmt.Fprintf(a.err, "Connecting to %s (%s)... type \"exit\" or \"~.\" at the start of a line to quit.\r\n", label, orDash(e.Type))

	// The shell outlives the default command timeout.
	err = shell.Run(context.WithoutCancel(ctx), shell.Options{
		URL:        wsURL,
		Header:     cl.ShellHeader(wsURL),
		HTTPClient: client.ShellHTTPClient(),
		Stdin:      attachStdin,
		Stdout:     attachOut,
		Size:       tty.Size,
		Escape:     true,
	})
	restore()
	switch {
	case errors.Is(err, shell.ErrEscape):
		fmt.Fprintln(a.err, "\nSession closed.")
		return nil
	case err != nil:
		return err
	}
	fmt.Fprintln(a.err, "\nConnection closed by the device.")
	return nil
}

// writableRoles returns the non-read-only privileges of the token that apply
// to the given org or site (MSP privileges always apply).
func writableRoles(raw json.RawMessage, orgID, siteID string) []string {
	var self struct {
		Privileges []map[string]any `json:"privileges"`
	}
	_ = json.Unmarshal(raw, &self)
	var out []string
	for _, p := range self.Privileges {
		role := strings.ToLower(fmt.Sprint(p["role"]))
		if role == "read" || role == "<nil>" {
			continue
		}
		scope := fmt.Sprint(p["scope"])
		pOrg, _ := p["org_id"].(string)
		pSite, _ := p["site_id"].(string)
		applies := false
		switch scope {
		case "msp":
			applies = true
		case "org":
			applies = pOrg == "" || orgID == "" || orgID == uuid.Nil.String() || pOrg == orgID
		case "site":
			applies = pSite == "" || pSite == siteID
		default:
			applies = true
		}
		if applies {
			name, _ := p["name"].(string)
			if name == "" {
				name = scope
			}
			out = append(out, output.Safe(fmt.Sprintf("%s %s (%s)", scope, name, role)))
		}
	}
	return out
}

func warnWritableShell(w io.Writer, roles []string) {
	bar := strings.Repeat("!", 72)
	fmt.Fprintln(w, bar)
	fmt.Fprintln(w, "!! WARNING: this token has WRITE privileges on this device:")
	for _, r := range roles {
		fmt.Fprintln(w, "!!   - "+r)
	}
	fmt.Fprintln(w, "!! The remote shell will be READ-WRITE: configuration changes and")
	fmt.Fprintln(w, "!! operational commands (reboot, request ...) will be applied.")
	fmt.Fprintln(w, "!! Use a token with the read-only (Observer) role for a read-only shell.")
	fmt.Fprintln(w, bar)
}
