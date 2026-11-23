package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/tokenfile"
)

const (
	tokenPrefix = "token_"
	imapPrefix  = "imap_"
	tokenSuffix = ".json"
)

// Registry is the set of signed-in accounts: one token file per account in
// the config directory. There is no second copy of the account list.
type Registry struct {
	dir string
}

// NewRegistry returns the registry stored in dir.
func NewRegistry(dir string) Registry {
	return Registry{dir: dir}
}

// TokenPath returns the token file path for an account.
func (r Registry) TokenPath(id ID) string {
	return filepath.Join(r.dir, tokenPrefix+id.DirName()+tokenSuffix)
}

// ErrNeedsLogin marks an account whose token file cannot be used; signing in
// again with login replaces it.
var ErrNeedsLogin = errors.New("sign-in needed")

// IDs returns every account with a well-named token file, sorted, whether or
// not the file's contents are valid. Use it to find an account to act on, such
// as removing a broken one. badNames reports token files whose names are not
// provider_email; err reports that the registry could not be read at all.
func (r Registry) IDs() (ids []ID, badNames, err error) {
	entries, err := os.ReadDir(r.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("read account registry %s: %w", r.dir, err)
	}

	var bad []error

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, tokenPrefix) || !strings.HasSuffix(name, tokenSuffix) {
			continue
		}

		id, parseErr := ParseDirName(strings.TrimSuffix(strings.TrimPrefix(name, tokenPrefix), tokenSuffix))
		if parseErr != nil {
			bad = append(bad, fmt.Errorf("token file %s: %w", name, parseErr))
			continue
		}

		ids = append(ids, id)
	}

	slices.SortFunc(ids, func(a, b ID) int { return strings.Compare(a.String(), b.String()) })

	return ids, errors.Join(bad...), nil
}

// List returns the accounts whose token file loads, sorted. Badly named,
// unreadable or invalid token files are reported in the returned error, which
// wraps ErrNeedsLogin for accounts that must sign in again; the valid accounts
// are still returned.
func (r Registry) List() ([]ID, error) {
	all, badNames, err := r.IDs()
	if err != nil {
		return nil, err
	}

	errs := []error{badNames}
	valid := make([]ID, 0, len(all))

	for _, id := range all {
		if _, loadErr := r.Token(id); loadErr != nil {
			errs = append(errs, loadErr)
			continue
		}

		valid = append(valid, id)
	}

	return valid, errors.Join(errs...)
}

// Token returns an account's saved token. A missing or invalid token file
// wraps ErrNeedsLogin.
func (r Registry) Token(id ID) (*oauth2.Token, error) {
	tok, err := tokenfile.Load(r.TokenPath(id))
	if err != nil {
		return nil, fmt.Errorf("account %s: %w: %w", id, ErrNeedsLogin, err)
	}

	return tok, nil
}

// Save stores an account's token, registering the account. Signing in again
// replaces the token.
func (r Registry) Save(id ID, tok *oauth2.Token) error {
	if err := tokenfile.Save(r.TokenPath(id), tok); err != nil {
		return fmt.Errorf("save account %s: %w", id, err)
	}

	return nil
}

// Remove deletes an account's token files. The account's archive and sync
// state are not touched.
func (r Registry) Remove(id ID) error {
	// The IMAP token goes first: once the main token is gone, the account
	// can no longer be named to remove what is left.
	if err := tokenfile.Delete(r.imapPath(id)); err != nil && !errors.Is(err, tokenfile.ErrNotFound) {
		return fmt.Errorf("remove account %s: %w", id, err)
	}

	if err := tokenfile.Delete(r.TokenPath(id)); err != nil {
		return fmt.Errorf("remove account %s: %w", id, err)
	}

	return nil
}

// imapPath is the file of an Outlook.com account's second token, for IMAP
// (O138, F51). It does not register an account.
func (r Registry) imapPath(id ID) string {
	return filepath.Join(r.dir, imapPrefix+id.DirName()+tokenSuffix)
}

// IMAPToken returns an Outlook.com account's IMAP token. A missing or
// invalid token file wraps ErrNeedsLogin.
func (r Registry) IMAPToken(id ID) (*oauth2.Token, error) {
	tok, err := tokenfile.Load(r.imapPath(id))
	if err != nil {
		return nil, fmt.Errorf("account %s: mail writing: %w: %w", id, ErrNeedsLogin, err)
	}

	return tok, nil
}

// SaveIMAP stores an Outlook.com account's IMAP token.
func (r Registry) SaveIMAP(id ID, tok *oauth2.Token) error {
	if err := tokenfile.Save(r.imapPath(id), tok); err != nil {
		return fmt.Errorf("save account %s: %w", id, err)
	}

	return nil
}
