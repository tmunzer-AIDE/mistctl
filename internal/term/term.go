// Package term wraps the local terminal for `mistctl attach`: raw mode,
// size, and ANSI output support on Windows consoles.
package term

import (
	"os"

	xterm "golang.org/x/term"
)

// Terminal is the local terminal attached to a remote shell.
type Terminal interface {
	// IsTerminal reports whether stdin and stdout are both terminals.
	IsTerminal() bool
	// MakeRaw puts the terminal in raw mode (and, on Windows, enables ANSI
	// escape processing on the console) and returns a restore function.
	MakeRaw() (restore func(), err error)
	// Size returns the terminal size in columns and rows.
	Size() (cols, rows int, ok bool)
}

// OS is the process' real terminal.
type OS struct{}

func (OS) IsTerminal() bool {
	return xterm.IsTerminal(int(os.Stdin.Fd())) && xterm.IsTerminal(int(os.Stdout.Fd()))
}

func (OS) MakeRaw() (func(), error) {
	restoreVT := enableVT(os.Stdout)
	st, err := xterm.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		restoreVT()
		return nil, err
	}
	return func() {
		_ = xterm.Restore(int(os.Stdin.Fd()), st)
		restoreVT()
	}, nil
}

func (OS) Size() (int, int, bool) {
	c, r, err := xterm.GetSize(int(os.Stdout.Fd()))
	if err != nil || c <= 0 || r <= 0 {
		return 0, 0, false
	}
	return c, r, true
}
