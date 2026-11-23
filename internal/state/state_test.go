package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/state"
)

func testID(t *testing.T) account.ID {
	t.Helper()

	id, err := account.Parse("gmail:user@gmail.example.com")
	require.NoError(t, err)

	return id
}

func TestLoad_MissingIsEmpty(t *testing.T) {
	st, err := state.NewStore(filepath.Join(t.TempDir(), "state")).Load(testID(t))
	require.NoError(t, err)
	assert.Nil(t, st.GmailMail)
}

func TestSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store := state.NewStore(dir)
	id := testID(t)

	want := state.Account{GmailMail: &state.GmailMail{HistoryID: "123", Labels: map[string]string{"L1": "Work"}}}
	require.NoError(t, store.Save(id, &want))

	got, err := store.Load(id)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	info, err := os.Stat(filepath.Join(dir, "gmail_user@gmail.example.com.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoad_CorruptReturnsEmptyAndError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gmail_user@gmail.example.com.json"), []byte("{"), 0o600))

	st, err := state.NewStore(dir).Load(testID(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode state")
	assert.Nil(t, st.GmailMail)
}
