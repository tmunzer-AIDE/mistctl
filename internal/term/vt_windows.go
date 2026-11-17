//go:build windows

package term

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT turns on ANSI escape sequence processing for a console output
// handle, needed by legacy consoles (Windows Terminal already has it).
func enableVT(f *os.File) func() {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
