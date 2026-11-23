// Package tokenfile reads and writes OAuth2 token files. A token file stores
// only the OAuth token, wrapped in a "token" key, with owner-only permissions.
//
// Adapted from github.com/tonimelisma/onedrive-go (internal/tokenfile).
package tokenfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/atomicfile"
)

// ErrNotFound is returned by Load and Delete when the token file does not exist.
var ErrNotFound = errors.New("token file not found")

// FilePerms restricts token files to owner-only read/write.
const FilePerms = 0o600

// DirPerms is used when creating the token directory.
const DirPerms = 0o700

// File is the on-disk format for token files. Contains only the OAuth token.
type File struct {
	Token *oauth2.Token `json:"token"`
}

// Load reads a saved token file. Returns ErrNotFound if the file does not exist.
func Load(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the caller owns the token path; reading it is the purpose.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}

	var tf File

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&tf); err != nil {
		return nil, fmt.Errorf("decode token file %s: %w", path, err)
	}

	if tf.Token == nil {
		return nil, fmt.Errorf("check token file %s: missing token field (re-login required)", path)
	}

	if tf.Token.AccessToken == "" && tf.Token.RefreshToken == "" {
		return nil, fmt.Errorf("check token file %s: empty credentials (re-login required)", path)
	}

	return tf.Token, nil
}

// Save writes a token file atomically (temp file in the same directory, fsync,
// rename, directory fsync) with 0600 permissions. Never logs token values.
func Save(path string, tok *oauth2.Token) error {
	data, err := marshalTokenFile(tok)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerms); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	if err := atomicfile.Write(path, data, FilePerms); err != nil {
		return fmt.Errorf("write token file %s: %w", path, err)
	}

	return nil
}

// Delete removes a token file. Returns ErrNotFound if the file does not exist.
func Delete(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("remove token file %s: %w", path, err)
	}

	return nil
}

func marshalTokenFile(tok *oauth2.Token) ([]byte, error) {
	if tok == nil {
		return nil, errors.New("save token file: refusing to save nil token")
	}

	data, err := json.MarshalIndent(File{Token: tok}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode token file: %w", err)
	}

	return data, nil
}
