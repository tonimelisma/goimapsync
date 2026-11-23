package wintz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every zone in the table must be one Go can load, or times would silently
// fall back to UTC.
func TestEveryMappedZoneLoads(t *testing.T) {
	loadOnce.Do(load)
	require.Greater(t, len(toIANA), 100)

	for windows, name := range toIANA {
		_, err := time.LoadLocation(name)
		assert.NoError(t, err, "%s → %s", windows, name)
	}
}
