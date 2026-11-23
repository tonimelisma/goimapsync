package cli

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// redrawInterval limits how often the terminal status line is redrawn.
	redrawInterval = 100 * time.Millisecond
	// progressInterval is how often progress is logged when stderr is not
	// a terminal.
	progressInterval = 30 * time.Second
)

// clearLine returns the cursor to the start of the row and clears it.
const clearLine = "\r\033[K"

// statusLine shows progress on stderr (D77): in a terminal, one line with a
// bar per account in progress, redrawn in place; otherwise an INFO log line
// per account at most every progressInterval. It is also the writer logs go
// through, so a log line in a terminal clears the status line, is written,
// and the status line is drawn again below it.
type statusLine struct {
	mu  sync.Mutex
	out io.Writer
	// width gives the terminal's width; nil when stderr is not a terminal.
	width  func() int
	now    func() time.Time
	logger *slog.Logger

	text     string               // the status line shown, in a terminal
	drawn    time.Time            // when it was last drawn
	accounts []string             // accounts with progress shown, in the order they started
	segments map[string]segment   // each account's latest progress
	reported map[string]time.Time // when each account's progress was last logged
	started  time.Time            // when progress logging started
}

// segment is one account's latest progress.
type segment struct {
	kind, unit  string
	done, total int
}

func newStatusLine(out io.Writer, width func() int, now func() time.Time) *statusLine {
	return &statusLine{
		out: out, width: width, now: now,
		segments: make(map[string]segment), reported: make(map[string]time.Time), started: now(),
	}
}

// Write writes log output, keeping the status line below it.
func (s *statusLine) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.text == "" {
		return s.out.Write(p) //nolint:wrapcheck // a plain pass-through writer.
	}

	if _, err := io.WriteString(s.out, clearLine); err != nil {
		return 0, err //nolint:wrapcheck // as above.
	}

	n, err := s.out.Write(p)
	if err != nil {
		return n, err //nolint:wrapcheck // as above.
	}

	_, err = io.WriteString(s.out, s.text)

	return n, err //nolint:wrapcheck // as above.
}

// progress reports how far one data type of an account is. Units count
// what was handled, successfully or not.
func (s *statusLine) progress(account, kind, unit string, done, total int) {
	if s.width != nil {
		s.mu.Lock()
		defer s.mu.Unlock()

		if _, shown := s.segments[account]; !shown {
			s.accounts = append(s.accounts, account)
		}

		s.segments[account] = segment{kind: kind, unit: unit, done: done, total: total}
		s.draw(done == total)

		return
	}

	if total == 0 {
		return
	}

	s.mu.Lock()
	last, ok := s.reported[account]
	if !ok {
		last = s.started
	}

	due := s.now().Sub(last) >= progressInterval

	if due {
		s.reported[account] = s.now()
	}
	s.mu.Unlock()

	// Logged outside the lock: the log goes through Write.
	if due && s.logger != nil {
		s.logger.Info("progress", slog.String("account", account), slog.String("data", kind),
			slog.Int("done", done), slog.Int("total", total), slog.String("unit", unit))
	}
}

// barWidth is the number of cells in the progress bar.
const barWidth = 20

// bar draws how much of total is done, as "[=====     ]".
func bar(done, total int) string {
	filled := barWidth
	if total > 0 && done < total {
		filled = barWidth * max(done, 0) / total
	}

	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled) + "]"
}

// segmentSeparator separates the accounts on the status line.
const segmentSeparator = " | "

// draw shows the accounts' progress as the terminal status line, at most
// every redrawInterval unless force is set. One account gets a bar and its
// counts; several share the width equally, each as its name, data type and
// percentage, with the name cut first. The line fits the terminal: a line
// that wrapped could not be redrawn in place. The caller holds s.mu.
func (s *statusLine) draw(force bool) {
	if !force && s.now().Sub(s.drawn) < redrawInterval {
		return
	}

	limit := s.width() - 1

	var line string

	if len(s.accounts) == 1 {
		account := s.accounts[0]
		seg := s.segments[account]
		line = cut(fmt.Sprintf("%s %s: %s %d of %d %s",
			account, seg.kind, bar(seg.done, seg.total), seg.done, seg.total, seg.unit), limit)
	} else {
		share := (limit - len(segmentSeparator)*(len(s.accounts)-1)) / max(len(s.accounts), 1)
		parts := make([]string, 0, len(s.accounts))

		for _, account := range s.accounts {
			seg := s.segments[account]
			progress := fmt.Sprintf(" %s %d%%", seg.kind, percent(seg.done, seg.total))
			name := cut(account, share-utf8.RuneCountInString(progress))
			parts = append(parts, cut(name+progress, share))
		}

		line = strings.Join(parts, segmentSeparator)
	}

	s.text = line
	s.drawn = s.now()
	_, _ = io.WriteString(s.out, clearLine+line) //nolint:errcheck // progress display only.
}

// cut shortens text to at most limit runes, ending a cut text with "…". A
// limit below one leaves the text as it is: the width is unknown.
func cut(text string, limit int) string {
	if limit < 1 || utf8.RuneCountInString(text) <= limit {
		return text
	}

	return string([]rune(text)[:limit-1]) + "…"
}

// whole is a finished data type's percentage.
const whole = 100

// percent is how much of total is done; nothing to do is done.
func percent(done, total int) int {
	if total <= 0 || done >= total {
		return whole
	}

	return whole * max(done, 0) / total
}

// drop removes an account's progress once its backup is done, so an account
// waiting to print its result does not keep space the others could use.
func (s *statusLine) drop(account string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, shown := s.segments[account]; !shown {
		return
	}

	delete(s.segments, account)
	s.accounts = slices.DeleteFunc(s.accounts, func(a string) bool { return a == account })

	if len(s.accounts) == 0 {
		s.erase()
	} else {
		s.draw(true)
	}
}

// clear removes the status line and every account's progress, before
// results are printed.
func (s *statusLine) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.accounts = nil
	clear(s.segments)
	s.erase()
}

// finish removes an account's progress and runs printResult, which prints the
// account's result, with the status line out of the way; the other
// accounts' progress is drawn again below it. Holding the lock keeps their
// progress from being drawn into the result's line.
func (s *statusLine) finish(account string, printResult func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, shown := s.segments[account]; shown {
		delete(s.segments, account)
		s.accounts = slices.DeleteFunc(s.accounts, func(a string) bool { return a == account })
	}

	s.erase()

	err := printResult()

	if s.width != nil && len(s.accounts) > 0 {
		s.draw(true)
	}

	return err
}

// erase clears the status line from the terminal. The caller holds s.mu.
func (s *statusLine) erase() {
	if s.text != "" {
		s.text = ""
		_, _ = io.WriteString(s.out, clearLine) //nolint:errcheck // progress display only.
	}
}
