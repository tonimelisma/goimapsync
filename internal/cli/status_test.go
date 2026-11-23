package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestStatusLine_Terminal(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, func() int { return 80 }, clock.now)
	logger := slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	}))

	clock.t = clock.t.Add(time.Second)
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 1, 3)
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 2, 3) // too soon: not drawn
	logger.Warn("slow")
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 3, 3) // the last one is always drawn
	s.clear()

	assert.Equal(t, clearLine+"gmail:a@gmail.example.com mail: [======              ] 1 of 3 messages"+
		clearLine+"level=WARN msg=slow\n"+"gmail:a@gmail.example.com mail: [======              ] 1 of 3 messages"+
		clearLine+"gmail:a@gmail.example.com mail: [====================] 3 of 3 messages"+
		clearLine, out.String())
}

func TestBar(t *testing.T) {
	assert.Equal(t, "[                    ]", bar(0, 7))
	assert.Equal(t, "[==========          ]", bar(5, 10))
	assert.Equal(t, "[====================]", bar(10, 10))
	assert.Equal(t, "[====================]", bar(0, 0), "nothing to do is done")
}

func TestStatusLine_NotTerminalLogsEvery30Seconds(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, nil, clock.now)
	s.logger = slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	}))

	s.progress("outlook:b@outlook.example.com", "mail", "messages", 1, 9) // a short run logs nothing
	clock.t = clock.t.Add(31 * time.Second)
	s.progress("outlook:b@outlook.example.com", "mail", "messages", 5, 9)
	clock.t = clock.t.Add(time.Second)
	s.progress("outlook:b@outlook.example.com", "mail", "messages", 6, 9)
	s.clear()

	assert.Equal(t, "level=INFO msg=progress account=outlook:b@outlook.example.com data=mail done=5 total=9 unit=messages\n",
		out.String())
}

func TestStatusLine_LineIsCutToTerminalWidth(t *testing.T) {
	var out bytes.Buffer

	s := newStatusLine(&out, func() int { return 40 }, time.Now)
	s.progress("gmail:a.very.long.address@gmail.example.com", "contacts", "address books", 1, 1)

	assert.Len(t, []rune(s.text), 39)
	assert.Equal(t, '…', []rune(s.text)[38])
}

func TestStatusLine_EmptyProgressIsNotLogged(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, nil, clock.now)
	s.logger = slog.New(slog.NewTextHandler(s, nil))
	clock.t = clock.t.Add(time.Minute)
	s.progress("gmail:a@gmail.example.com", "calendars", "calendars", 0, 0)

	assert.Empty(t, out.String())
}

// Accounts in progress share the line; a finished account's result is
// printed with the line out of the way, and the others are drawn again.
func TestStatusLine_TerminalAccountsShareTheLine(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, func() int { return 200 }, clock.now)

	clock.t = clock.t.Add(time.Second)
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 1, 2)
	clock.t = clock.t.Add(time.Second)
	s.progress("outlook:b@outlook.example.com", "mail", "messages", 1, 4)

	err := s.finish("gmail:a@gmail.example.com", func() error {
		_, err := out.WriteString("RESULT\n")

		return err //nolint:wrapcheck // test writer.
	})
	require.NoError(t, err)

	assert.Equal(t, clearLine+"gmail:a@gmail.example.com mail: [==========          ] 1 of 2 messages"+
		clearLine+"gmail:a@gmail.example.com mail 50% | outlook:b@outlook.example.com mail 25%"+
		clearLine+"RESULT\n"+
		clearLine+"outlook:b@outlook.example.com mail: [=====               ] 1 of 4 messages", out.String())
}

// Several accounts share a narrow terminal equally; names are cut, never
// the progress, and an account that is done leaves the line at once.
func TestStatusLine_TerminalNarrowSharesWidth(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, func() int { return 81 }, clock.now)

	s.progress("gmail:anna.exa@example.com", "mail", "messages", 23, 100)
	s.progress("gmail:anna.anothers@example.com", "contacts", "contacts", 4, 4)
	clock.t = clock.t.Add(time.Second)
	s.progress("outlook:anna@example.org", "mail", "messages", 1, 2)

	line := s.text
	assert.LessOrEqual(t, utf8.RuneCountInString(line), 80)
	assert.Equal(t, "gmail:anna.exa… mail 23% | gmail:ann… contacts 100% | outlook:anna@e… mail 50%", line)

	s.drop("gmail:anna.anothers@example.com")
	assert.Equal(t, "gmail:anna.exa@example.com mail 23% | outlook:anna@example.org mail 50%", s.text)

	s.drop("gmail:anna.exa@example.com")
	s.drop("outlook:anna@example.org")
	assert.Empty(t, s.text)
	assert.True(t, strings.HasSuffix(out.String(), clearLine))
}

// Without a terminal each account's progress is logged on its own clock, so
// one busy account does not keep another's progress out of the log.
func TestStatusLine_NotTerminalLogsEachAccount(t *testing.T) {
	var out bytes.Buffer

	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newStatusLine(&out, nil, clock.now)
	s.logger = slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	}))

	clock.t = clock.t.Add(31 * time.Second)
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 5, 9)
	s.progress("outlook:b@outlook.example.com", "mail", "messages", 2, 9)
	s.progress("gmail:a@gmail.example.com", "mail", "messages", 6, 9) // too soon for this account

	assert.Equal(t, "level=INFO msg=progress account=gmail:a@gmail.example.com data=mail done=5 total=9 unit=messages\n"+
		"level=INFO msg=progress account=outlook:b@outlook.example.com data=mail done=2 total=9 unit=messages\n",
		out.String())
}
