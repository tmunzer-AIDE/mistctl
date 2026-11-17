package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/tmunzer-AIDE/mistctl/internal/term"
)

// fakeShell is the device side of the websocket: it prints a prompt, echoes
// one command and closes.
func fakeShell(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	if _, _, err := c.Read(ctx); err != nil { // resize
		return
	}
	_ = c.Write(ctx, websocket.MessageBinary, []byte("jane@sw-core> "))
	_, b, err := c.Read(ctx)
	if err != nil {
		return
	}
	_ = c.Write(ctx, websocket.MessageBinary, []byte(fmt.Sprintf("got %q\r\n", b)))
	c.Close(websocket.StatusNormalClosure, "")
}

type fakeTerm struct {
	tty      bool
	mu       sync.Mutex
	raw      bool
	restored bool
}

func (f *fakeTerm) IsTerminal() bool { return f.tty }
func (f *fakeTerm) MakeRaw() (func(), error) {
	f.mu.Lock()
	f.raw = true
	f.mu.Unlock()
	return func() { f.mu.Lock(); f.restored = true; f.mu.Unlock() }, nil
}
func (f *fakeTerm) Size() (int, int, bool) { return 80, 24, true }

func withTerm(t *testing.T, ft *fakeTerm, stdin string) *bytes.Buffer {
	t.Helper()
	oldT, oldIn, oldOut := newTerminal, attachStdin, attachOut
	out := &bytes.Buffer{}
	newTerminal = func() term.Terminal { return ft }
	attachStdin = strings.NewReader(stdin)
	attachOut = out
	t.Cleanup(func() { newTerminal, attachStdin, attachOut = oldT, oldIn, oldOut })
	return out
}

func TestAttachSwitch(t *testing.T) {
	fake := setup(t)
	ft := &fakeTerm{tty: true}
	out := withTerm(t, ft, "show version\r")
	msgs := run(t, "attach", "sw-core")
	if got := out.String(); !strings.Contains(got, "jane@sw-core> ") || !strings.Contains(got, `got "\x00show version\r"`) {
		t.Fatalf("terminal output = %q", got)
	}
	if !ft.raw || !ft.restored {
		t.Fatalf("terminal raw=%v restored=%v", ft.raw, ft.restored)
	}
	assertContains(t, msgs, "Connecting to sw-core (switch)", "Connection closed by the device")
	if strings.Contains(msgs, "WARNING") {
		t.Errorf("unexpected write warning for a read-only org privilege:\n%s", msgs)
	}
	calls, methods := fake.snapshot()
	posts := 0
	for i, m := range methods {
		if m != "GET" {
			posts++
			if m != "POST" || !strings.HasSuffix(strings.Split(calls[i], "?")[0], "/devices/"+swID+"/shell") {
				t.Errorf("unexpected %s %s", m, calls[i])
			}
		}
	}
	if posts != 1 {
		t.Errorf("expected exactly one POST, got %d", posts)
	}
}

func TestAttachRefusals(t *testing.T) {
	fake := setup(t)
	withTerm(t, &fakeTerm{tty: true}, "")
	if _, err := runErr("attach", "ap-lobby"); err == nil || !strings.Contains(err.Error(), "access point") {
		t.Errorf("attach to an AP: %v", err)
	}
	if _, err := runErr("attach", "sw-core", "--node", "node3"); err == nil || !strings.Contains(err.Error(), "--node") {
		t.Errorf("bad --node: %v", err)
	}
	withTerm(t, &fakeTerm{tty: false}, "")
	if _, err := runErr("attach", "sw-core"); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Errorf("attach without a TTY: %v", err)
	}
	_, methods := fake.snapshot()
	for _, m := range methods {
		if m != "GET" {
			t.Errorf("refused attach still sent a %s", m)
		}
	}
}

func TestWritableRoles(t *testing.T) {
	raw := json.RawMessage(fmt.Sprintf(`{"privileges":[
		{"scope":"org","org_id":"%s","name":"ACME","role":"read"},
		{"scope":"site","org_id":"%s","site_id":"%s","name":"HQ","role":"write"},
		{"scope":"site","org_id":"%s","site_id":"%s","name":"Lab","role":"admin"},
		{"scope":"org","org_id":"%s","name":"Globex","role":"admin"}]}`,
		orgID, orgID, siteHQ, orgID, siteLab, org2ID))
	got := writableRoles(raw, orgID, siteHQ)
	if len(got) != 1 || got[0] != "site HQ (write)" {
		t.Fatalf("writableRoles = %v", got)
	}
	got = writableRoles(json.RawMessage(`{"privileges":[{"scope":"msp","msp_id":"x","name":"MSP","role":"admin"}]}`), orgID, siteHQ)
	if len(got) != 1 {
		t.Fatalf("MSP admin should count as writable: %v", got)
	}
	var b bytes.Buffer
	warnWritableShell(&b, got)
	assertContains(t, b.String(), "WRITE privileges", "READ-WRITE", "MSP (admin)")
}
