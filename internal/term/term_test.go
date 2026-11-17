package term

import (
	"os"
	"testing"
)

// Under `go test` stdin is not a terminal: the wrappers must say so and
// fail cleanly instead of touching the terminal state.
func TestNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, w
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()

	var tty Terminal = OS{}
	if tty.IsTerminal() {
		t.Fatal("a pipe is not a terminal")
	}
	if restore, err := tty.MakeRaw(); err == nil {
		restore()
		t.Fatal("MakeRaw on a pipe must fail")
	}
	if _, _, ok := tty.Size(); ok {
		t.Fatal("a pipe has no size")
	}
}
