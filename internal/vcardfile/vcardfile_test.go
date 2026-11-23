package vcardfile_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/vcardfile"
)

func TestEncode_EscapingAndRoundTrip(t *testing.T) {
	c := vcardfile.New("people/c1")
	c.SetText("FN", "Ann; B, \\ C")
	c.Structured("N", []string{"Smith;Jones", "Ann", "", "", ""})
	c.Text("NOTE", "line one\r\nline two")
	c.Text("EMAIL", "ann@example.com", vcardfile.Type("work"), vcardfile.Pref())
	c.Text("EMPTY", "")
	c.URI("URL", "https://example.com/a,b;c")
	c.Photo([]byte{0xff, 0xd8, 0xff}, "image/jpeg")
	c.Categories([]string{"Work", "Family, close"})
	c.Text("X-LABEL", "v", vcardfile.Param{Name: "LABEL", Value: `a "b": c`})

	data := c.Encode()

	card, err := vcard.NewDecoder(bytes.NewReader(data)).Decode()
	require.NoError(t, err)
	assert.Equal(t, "Ann; B, \\ C", card.Value(vcard.FieldFormattedName))
	assert.Equal(t, "line one\nline two", card.Value(vcard.FieldNote))
	assert.Equal(t, []string{"Family, close", "Work"}, card.Values(vcard.FieldCategories))
	assert.Equal(t, "https://example.com/a,b;c", card.Value(vcard.FieldURL))
	assert.Equal(t, "data:image/jpeg;base64,/9j/", card.Value(vcard.FieldPhoto))
	assert.Equal(t, "1", card.Get(vcard.FieldEmail).Params.Get(vcard.ParamPreferred))
	assert.Nil(t, card.Get("EMPTY"))

	text := string(data)
	assert.Contains(t, text, "N:Smith\\;Jones;Ann;;;\r\n", "a semicolon inside a part is escaped")
	assert.Contains(t, text, "URL:https://example.com/a,b;c\r\n", "URI values are not escaped")
	assert.Contains(t, text, "X-LABEL;LABEL=\"a 'b': c\":v\r\n")
	assert.True(t, strings.HasPrefix(text, "BEGIN:VCARD\r\nVERSION:4.0\r\n"))
}

func TestEncode_FoldsLongLines(t *testing.T) {
	c := vcardfile.New("x")
	c.Text("NOTE", strings.Repeat("ä", 100))

	data := string(c.Encode())

	for _, line := range strings.Split(strings.TrimSuffix(data, "\r\n"), "\r\n") {
		assert.LessOrEqual(t, len(line), 75, line)
	}

	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("ä", 100), card.Value(vcard.FieldNote))
}
