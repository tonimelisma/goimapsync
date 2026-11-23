package tokenfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func testToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  "access-test",
		RefreshToken: "refresh-test",
		TokenType:    "Bearer",
		Expiry:       time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func writeRaw(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, os.WriteFile(path, []byte(content), FilePerms))

	return path
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Parallel()

	tok, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	assert.Nil(t, tok)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLoad_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	original := testToken()
	require.NoError(t, Save(path, original))

	tok, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, original.AccessToken, tok.AccessToken)
	assert.Equal(t, original.RefreshToken, tok.RefreshToken)
	assert.Equal(t, original.TokenType, tok.TokenType)
	assert.True(t, tok.Expiry.Equal(original.Expiry))
}

func TestLoad_RejectsMalformedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "bare token without wrapper", content: `{"access_token":"a","refresh_token":"r"}`, wantErr: "decode token file"},
		{name: "invalid json", content: `{not json}`, wantErr: "decode token file"},
		{name: "unknown keys", content: `{"token":{"access_token":"a"},"extra":1}`, wantErr: "decode token file"},
		{name: "missing token field", content: `{}`, wantErr: "missing token field"},
		{name: "empty credentials", content: `{"token":{"token_type":"Bearer"}}`, wantErr: "empty credentials"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tok, err := Load(writeRaw(t, tt.content))
			assert.Nil(t, tok)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSave_CreatesDirectoryWithOwnerOnlyPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "dir", "token.json")
	require.NoError(t, Save(path, testToken()))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(FilePerms), info.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(DirPerms), dirInfo.Mode().Perm())
}

func TestSave_LeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, Save(filepath.Join(dir, "token.json"), testToken()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "token.json", entries[0].Name())
}

func TestSave_OverwritesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, Save(path, testToken()))

	updated := testToken()
	updated.AccessToken = "access-updated"
	require.NoError(t, Save(path, updated))

	tok, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "access-updated", tok.AccessToken)
}

func TestSave_InvalidParentDirectory(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), FilePerms))

	err := Save(filepath.Join(blocker, "token.json"), testToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create token directory")
}

func TestSave_FailedRenameLeavesNoTempFile(t *testing.T) {
	t.Parallel()

	// A non-empty directory at the target path makes the final rename fail
	// after the temp file was written, even when running as root.
	dir := t.TempDir()
	target := filepath.Join(dir, "token.json")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), DirPerms))

	err := Save(target, testToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rename temp file")

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Len(t, entries, 1)
	assert.Equal(t, "token.json", entries[0].Name())
}

func TestSave_NilToken(t *testing.T) {
	t.Parallel()

	err := Save(filepath.Join(t.TempDir(), "token.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to save nil token")
}

func TestSave_WritesOnlyTheTokenKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, Save(path, testToken()))

	data, err := os.ReadFile(path) //nolint:gosec // G304: test-owned temp path.
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Len(t, raw, 1)
	assert.Contains(t, raw, "token")
}

func TestDelete(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, Save(path, testToken()))
	require.NoError(t, Delete(path))

	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, Delete(path), ErrNotFound)
}
