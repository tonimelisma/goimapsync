package restore

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/mailheader"
)

func TestHeaderReceived(t *testing.T) {
	h := mailheader.Parse([]byte("Received: by x; not a date\r\n" +
		"Received: from a by b\r\n  with SMTP;\r\n  Tue, 3 Mar 2026 09:00:00 +0200 (EET)\r\n" +
		"Received: from c by a; Tue, 3 Mar 2026 08:00:00 +0200\r\n\r\n"))

	got, ok := received(h)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 3, 3, 7, 0, 0, 0, time.UTC), got.UTC(), "the newest Received field with a date")

	_, ok = received(mailheader.Parse([]byte("Received: by x\r\n\r\n")))
	assert.False(t, ok)
}

func TestRepaired_LeavesACompleteMessageAlone(t *testing.T) {
	raw := []byte("Message-ID: <a@x>\r\nDate: Mon, 02 Mar 2026 10:00:00 +0000\r\n\r\nhi\r\n")

	got, fileDated, err := repaired(raw, func() (time.Time, error) { panic("not needed") })
	require.NoError(t, err)
	assert.Equal(t, raw, got)
	assert.False(t, fileDated)
}

func TestRepaired_ReplacesAFoldedBadDate(t *testing.T) {
	modified := time.Date(2026, 4, 1, 12, 0, 0, 0, time.FixedZone("EEST", 3*3600))

	got, fileDated, err := repaired([]byte("Message-ID: <a@x>\r\nDate: someday\r\n soon\r\nSubject: s\r\n\r\nhi\r\n"),
		func() (time.Time, error) { return modified, nil })
	require.NoError(t, err)
	assert.True(t, fileDated)
	assert.Equal(t, "Date: Wed, 01 Apr 2026 09:00:00 +0000\r\nMessage-ID: <a@x>\r\nSubject: s\r\n\r\nhi\r\n", string(got))
}

func TestRepaired_DerivedMessageIDIsStable(t *testing.T) {
	raw := []byte("Date: Mon, 02 Mar 2026 10:00:00 +0000\r\n\r\nhi\r\n")

	first, _, err := repaired(raw, nil)
	require.NoError(t, err)

	second, _, err := repaired(raw, nil)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	other, _, err := repaired([]byte("Date: Mon, 02 Mar 2026 10:00:00 +0000\r\n\r\nho\r\n"), nil)
	require.NoError(t, err)
	assert.NotEqual(t, first, other, "derived from the content")

	key, ok := keyOf(first)
	require.True(t, ok)
	assert.True(t, validMessageID(key.messageID), key.messageID)
}

func TestRepaired_FailsWithoutAnyDate(t *testing.T) {
	_, _, err := repaired([]byte("Message-ID: <a@x>\r\n\r\nhi\r\n"), func() (time.Time, error) { return time.Time{}, errors.New("gone") })
	require.ErrorContains(t, err, "gone")
}

func TestRepaired_ReplacesAnEmptyMessageID(t *testing.T) {
	got, _, err := repaired([]byte("Date: Mon, 02 Mar 2026 10:00:00 +0000\r\nMessage-ID:\r\n  \r\nSubject: s\r\n\r\nhi\r\n"), nil)
	require.NoError(t, err)
	assert.Regexp(t, `^Message-ID: <[0-9a-f]{32}@pimvault\.invalid>\r\nDate: Mon, 02 Mar 2026 10:00:00 \+0000\r\nSubject: s\r\n\r\nhi\r\n$`, string(got))
}
