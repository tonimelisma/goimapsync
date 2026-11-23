// Command pimvault backs up Gmail and Outlook.com mail, calendars, contacts and
// tasks to local files, and restores or migrates accounts from them.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"unsafe"

	// Event times are written in their IANA time zones; embedding the zone
	// database makes that work on servers without /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/tonimelisma/pimvault/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // config resolution reports it if no override is given
	}

	code := cli.Run(ctx, os.Args[1:], &cli.Runtime{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Env:         os.LookupEnv,
		Home:        home,
		StderrWidth: terminalWidth(os.Stderr),
		Version:     version(),
	})

	stop()
	os.Exit(code)
}

// version returns the version Go recorded in the build: the tag for a
// build of a tagged commit (script/release.sh), a pseudo-version with the
// commit otherwise.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Version
	}

	return "unknown"
}

// terminalWidth returns a function giving f's width in columns when f is
// a terminal that can redraw a line in place, or nil.
func terminalWidth(f *os.File) func() int {
	if os.Getenv("TERM") == "dumb" {
		return nil
	}

	width := func() int {
		var size struct{ rows, cols, x, y uint16 }

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ,
			uintptr(unsafe.Pointer(&size))) //nolint:gosec // G103: the ioctl needs a pointer to its result.
		if errno != 0 {
			return 0
		}

		return int(size.cols)
	}

	if width() == 0 {
		return nil
	}

	return width
}
