package wintz_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tonimelisma/pimvault/internal/wintz"
)

// Validates: PV-179
func TestIANA(t *testing.T) {
	for windows, want := range map[string]string{
		"FLE Standard Time":       "Europe/Kiev",
		"Pacific Standard Time":   "America/Los_Angeles",
		"UTC":                     "Etc/UTC",
		"W. Europe Standard Time": "Europe/Berlin",
	} {
		got, ok := wintz.IANA(windows)
		assert.True(t, ok, windows)
		assert.Equal(t, want, got, windows)
	}

	_, ok := wintz.IANA("tzone://Microsoft/Custom")
	assert.False(t, ok)
}

func TestLocation(t *testing.T) {
	loc, ok := wintz.Location("FLE Standard Time")
	assert.True(t, ok)
	assert.Equal(t, "Europe/Kiev", loc.String())

	again, _ := wintz.Location("FLE Standard Time")
	assert.Same(t, loc, again)

	_, ok = wintz.Location("nowhere")
	assert.False(t, ok)
}
