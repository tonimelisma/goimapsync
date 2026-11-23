package restore

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidMessageID(t *testing.T) {
	for id, want := range map[string]bool{
		"<a.b+c@example.com>":          true,
		"<a@[127.0.0.1]>":              true,
		"<seed-1@pimvault.example>":    true,
		"<Share-1;rcid_2@x>":           false,
		"<[fd2a]@microsoft.com>":       false,
		"<a@b@c>":                      false,
		"<a..b@c>":                     false,
		"<a b@c>":                      false,
		"a@b":                          false,
		"<a@b":                         false,
		"<>":                           false,
		"<MassDelete-1;rcid_2;wiid_3>": false,
	} {
		assert.Equal(t, want, validMessageID(id), id)
	}
}

func TestHasEncodedWordAndSameMessageID(t *testing.T) {
	assert.True(t, hasEncodedWord("<=?utf-8?b?aDE3?=-h17@x>"))
	assert.False(t, hasEncodedWord("<h18?q@x>"))
	assert.False(t, hasEncodedWord("<h19=e@x>"))
	assert.False(t, hasEncodedWord("<a=?b@x>"), "an opening without a close")
	assert.False(t, hasEncodedWord("<a=?b?=c@x>"), "no charset?encoding?text: not an encoded-word")

	assert.True(t, sameMessageID("<=?utf-8?b?aDE3?=-h17@x>", "<h17-h17@x>"), "given decoded")
	assert.True(t, sameMessageID("<=?utf-8?b?aDE3?=-h17@x>", "<=?utf-8?b?aDE3?=-\r\n h17@x>"), "folded")
	assert.False(t, sameMessageID("<=?utf-8?b?aDE3?=-h17@x>", "<=?utf-8?b?aDE4?=-h17@x>"))
}

func TestNormalFrom(t *testing.T) {
	assert.Equal(t, "l@example.com", normalFrom("=?iso-8859-2?Q?=A3ukasz_W=F3jcik?= <L@example.com>"))
	assert.Equal(t, "l@example.com", normalFrom(`"Łukasz Wójcik" <l@example.com>`))
	assert.Equal(t, "j@x.example", normalFrom("Doe, John <J@x.example>"), "a decoded name that does not parse")
	assert.Equal(t, "not an address", normalFrom(" not an address "))
}

func TestRestoredName(t *testing.T) {
	assert.Equal(t, "Chats (restored)", restoredName("Chats", nameLimit{runes: 225}))
	assert.Equal(t, strings.Repeat("ö", 214)+" (restored)", restoredName(strings.Repeat("ö", 300), nameLimit{runes: 225}), "cut in characters")
	assert.Equal(t, strings.Repeat("ö", 122)+" (restored)", restoredName(strings.Repeat("ö", 300), nameLimit{runes: 225, bytes: 255}),
		"and in bytes")
}

func TestKeyOf_FoldedMessageIDLosesTheFold(t *testing.T) {
	key, ok := keyOf([]byte("Message-ID: <Share-1;rcid_2;\r\n wiid_3@x>\r\nDate: Mon, 02 Mar 2026 10:00:00 +0000\r\nFrom: a@b\r\n\r\nhi\r\n"))
	require.True(t, ok)
	assert.Equal(t, "<Share-1;rcid_2;wiid_3@x>", key.messageID)
}

func hourly(start time.Time, count int) func() (time.Time, bool) {
	n := 0

	return func() (time.Time, bool) {
		if count > 0 && n == count {
			return time.Time{}, false
		}

		n++

		return start.Add(time.Duration(n) * time.Hour), true
	}
}

func TestExpandOccurrences_OpenSeriesKeepsTheLatest(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got, capped := expandOccurrences(hourly(start, 0), start.Add(1000*time.Hour))
	require.Len(t, got, maxExpanded)
	assert.True(t, capped)
	assert.Equal(t, start.Add(1000*time.Hour), got[len(got)-1])
	assert.Equal(t, start.Add((1001-maxExpanded)*time.Hour), got[0])
}

func TestExpandOccurrences_OpenSeriesAtTheCapIsWhole(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got, capped := expandOccurrences(hourly(start, 0), start.Add(maxExpanded*time.Hour))
	assert.Len(t, got, maxExpanded)
	assert.False(t, capped)
}

func TestExpandOccurrences_BoundedSeriesKeepsTheLatest(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got, capped := expandOccurrences(hourly(start, 1200), time.Time{})
	require.Len(t, got, maxExpanded)
	assert.True(t, capped)
	assert.Equal(t, start.Add(1200*time.Hour), got[len(got)-1])
}

func TestExpandOccurrences_ShortBoundedSeriesIsWhole(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got, capped := expandOccurrences(hourly(start, 30), time.Time{})
	assert.Len(t, got, 30)
	assert.False(t, capped)
}

func TestExpandOccurrences_StopsAfterMaxIterations(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got, capped := expandOccurrences(hourly(start, maxIterations+1), time.Time{})
	assert.Len(t, got, maxExpanded)
	assert.True(t, capped)
}
