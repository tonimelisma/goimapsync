package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLatest_LastAppearanceWins(t *testing.T) {
	gone := &struct{}{}
	all := []Contact{
		{ID: "a", DisplayName: "Ann"},
		{ID: "b", DisplayName: "Bob"},
		{ID: "a", Removed: gone}, // edited, then deleted
		{ID: "c", Removed: gone},
		{ID: "c", DisplayName: "Cid"}, // deleted, then back
		{ID: "b", DisplayName: "Bob Smith"},
	}

	got := latest[Contact](all)

	assert.Equal(t, []string{"a"}, got.Removed)
	assert.Equal(t, []Contact{{ID: "c", DisplayName: "Cid"}, {ID: "b", DisplayName: "Bob Smith"}}, got.Changed)
}
