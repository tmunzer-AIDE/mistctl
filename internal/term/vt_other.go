//go:build !windows

package term

import "os"

func enableVT(*os.File) func() { return func() {} }
