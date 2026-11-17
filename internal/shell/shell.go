// Package shell pipes a Mist remote shell websocket to a terminal.
//
// Framing (same as the mistapi Python package): keystrokes are sent as binary
// frames prefixed with a 0x00 byte, terminal size changes as a text frame
// {"resize":{"width":W,"height":H}}, and everything the device sends is raw
// terminal output.
package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// SizeFunc returns the current terminal size in columns and rows.
type SizeFunc func() (cols, rows int, ok bool)

// Options configures a shell session.
type Options struct {
	URL    string
	Header http.Header
	// HTTPClient is used for the websocket handshake (nil: default client).
	HTTPClient *http.Client
	Stdin      io.Reader
	Stdout     io.Writer
	Size       SizeFunc
	// ResizePoll is how often the terminal size is checked (default 250ms).
	// Polling works the same on every OS, including Windows.
	ResizePoll time.Duration
	// ReadyTimeout is how long to hold keystrokes back while waiting for the
	// first output of the device (default 10s).
	ReadyTimeout time.Duration
	// Escape enables the ssh-style "~." (at the start of a line) sequence
	// to end the session locally.
	Escape bool
}

// ErrEscape is returned when the user ended the session with "~.".
var ErrEscape = errors.New("session closed with ~.")

// Run connects to the shell and copies data both ways until the device
// closes the session, the user types the escape sequence, reading stdin
// fails or ctx is cancelled. A normal close by the device returns nil.
func Run(ctx context.Context, o Options) error {
	if o.ResizePoll <= 0 {
		o.ResizePoll = 250 * time.Millisecond
	}
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, o.URL, &websocket.DialOptions{HTTPHeader: o.Header, HTTPClient: o.HTTPClient})
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket handshake failed: %s", resp.Status)
		}
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)

	cols, rows := 80, 24
	if o.Size != nil {
		if c, r, ok := o.Size(); ok {
			cols, rows = c, r
		}
	}
	if err := sendResize(ctx, conn, cols, rows); err != nil {
		return fmt.Errorf("websocket send failed: %w", err)
	}

	done := make(chan error, 3)
	ready := make(chan struct{})

	// device -> terminal
	go func() {
		first := true
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				done <- readErr(err)
				return
			}
			if first && len(data) > 0 {
				close(ready)
				first = false
			}
			if _, err := o.Stdout.Write(data); err != nil {
				done <- err
				return
			}
		}
	}()

	// terminal -> device, once the shell is ready (keystrokes typed earlier
	// stay in the terminal's input buffer).
	go func() {
		select {
		case <-ready:
		case <-time.After(o.ReadyTimeout):
		case <-ctx.Done():
			return
		}
		esc := escaper{lineStart: true, enabled: o.Escape}
		buf := make([]byte, 4096)
		for {
			n, err := o.Stdin.Read(buf)
			if n > 0 {
				out, quit := esc.feed(buf[:n])
				if len(out) > 0 {
					if werr := conn.Write(ctx, websocket.MessageBinary, append([]byte{0}, out...)); werr != nil {
						done <- readErr(werr)
						return
					}
				}
				if quit {
					done <- ErrEscape
					return
				}
			}
			if err != nil {
				// On EOF stop sending but keep showing output until the
				// device closes the session.
				if !errors.Is(err, io.EOF) {
					done <- err
				}
				return
			}
		}
	}()

	// terminal size changes
	if o.Size != nil {
		go func() {
			t := time.NewTicker(o.ResizePoll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					c, r, ok := o.Size()
					if !ok || (c == cols && r == rows) {
						continue
					}
					cols, rows = c, r
					if err := sendResize(ctx, conn, cols, rows); err != nil {
						done <- readErr(err)
						return
					}
				}
			}
		}()
	}

	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return err
}

func sendResize(ctx context.Context, conn *websocket.Conn, cols, rows int) error {
	b, _ := json.Marshal(map[string]any{"resize": map[string]int{"width": cols, "height": rows}})
	return conn.Write(ctx, websocket.MessageText, b)
}

// readErr turns a normal websocket close into nil.
func readErr(err error) error {
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return nil
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// escaper implements the ssh-style escape: "~." at the start of a line ends
// the session, "~~" sends a single "~".
type escaper struct {
	enabled   bool
	lineStart bool
	tilde     bool
}

func (e *escaper) feed(in []byte) (out []byte, quit bool) {
	if !e.enabled {
		return in, false
	}
	out = make([]byte, 0, len(in)+1)
	for _, b := range in {
		if e.tilde {
			e.tilde = false
			switch b {
			case '.':
				return out, true
			case '~':
				out = append(out, '~')
				e.lineStart = false
				continue
			default:
				out = append(out, '~')
			}
		} else if e.lineStart && b == '~' {
			e.tilde = true
			continue
		}
		out = append(out, b)
		e.lineStart = b == '\r' || b == '\n'
	}
	return out, false
}
