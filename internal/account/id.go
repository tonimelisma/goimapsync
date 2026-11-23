// Package account owns account identity (provider:email, taken from the
// sign-in) and the account registry, which is the set of token files in the
// config directory. It does not sign in or talk to providers.
package account

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Provider is a supported account provider.
type Provider string

// Supported providers.
const (
	Gmail   Provider = "gmail"
	Outlook Provider = "outlook"
)

var errUnknownProvider = errors.New("unknown provider")

// ParseProvider validates a provider name.
func ParseProvider(s string) (Provider, error) {
	switch p := Provider(s); p {
	case Gmail, Outlook:
		return p, nil
	default:
		return "", fmt.Errorf("parse provider %q: %w (want %s or %s)", s, errUnknownProvider, Gmail, Outlook)
	}
}

// ID identifies an account as provider:email. The provider is part of the
// identity because a Microsoft account can sign in with a Gmail address. The
// email is stored in lower case: providers treat addresses case-insensitively,
// so the same mailbox must never become two accounts.
type ID struct {
	Provider Provider
	Email    string
}

// Parse parses the provider:email form.
func Parse(s string) (ID, error) {
	providerName, email, found := strings.Cut(s, ":")
	if !found {
		return ID{}, fmt.Errorf("parse account %q: want provider:email", s)
	}

	provider, err := ParseProvider(providerName)
	if err != nil {
		return ID{}, err
	}

	if !validEmail(email) {
		return ID{}, fmt.Errorf("parse account %q: invalid email", s)
	}

	return ID{Provider: provider, Email: strings.ToLower(email)}, nil
}

// String returns the provider:email form used in commands and output.
func (id ID) String() string {
	return string(id.Provider) + ":" + id.Email
}

// DirName returns the name used for this account's directories and files:
// provider_email, with ':' replaced by '_'.
func (id ID) DirName() string {
	return string(id.Provider) + "_" + id.Email
}

// ParseDirName reverses DirName. Provider names contain no '_', so the first
// '_' separates provider and email.
func ParseDirName(s string) (ID, error) {
	providerName, email, found := strings.Cut(s, "_")
	if !found {
		return ID{}, fmt.Errorf("parse account name %q: want provider_email", s)
	}

	return Parse(providerName + ":" + email)
}

// validEmail is a minimal sanity check: one '@' with text on both sides and
// no characters that are unsafe in file names or the provider:email form.
func validEmail(s string) bool {
	local, domain, found := strings.Cut(s, "@")

	return found && local != "" && domain != "" &&
		!strings.ContainsAny(s, "/\\: \t\n") && !strings.Contains(domain, "@")
}

// ErrNoMatch is returned by Match when no account matches the query.
var ErrNoMatch = errors.New("no matching account")

// ErrAmbiguous is returned by Match when several accounts match the query.
var ErrAmbiguous = errors.New("ambiguous account")

// Match finds the account a user meant: an exact provider:email match first,
// then a unique prefix, then a unique substring. Matching is case-insensitive.
func Match(ids []ID, query string) (ID, error) {
	q := strings.ToLower(query)
	if q == "" {
		return ID{}, fmt.Errorf("match account: %w: empty query", ErrNoMatch)
	}

	for _, id := range ids {
		if strings.ToLower(id.String()) == q {
			return id, nil
		}
	}

	for _, matches := range [][]ID{
		filterIDs(ids, func(s string) bool { return strings.HasPrefix(s, q) }),
		filterIDs(ids, func(s string) bool { return strings.Contains(s, q) }),
	} {
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			return ID{}, fmt.Errorf("match account %q: %w: %s", query, ErrAmbiguous, joinIDs(matches))
		}
	}

	return ID{}, fmt.Errorf("match account %q: %w", query, ErrNoMatch)
}

func filterIDs(ids []ID, keep func(lower string) bool) []ID {
	var out []ID

	for _, id := range ids {
		if keep(strings.ToLower(id.String())) {
			out = append(out, id)
		}
	}

	return out
}

func joinIDs(ids []ID) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, id.String())
	}

	slices.Sort(names)

	return strings.Join(names, ", ")
}
