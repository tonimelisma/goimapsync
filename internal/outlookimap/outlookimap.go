// Package outlookimap appends messages to an Outlook.com mailbox over IMAP
// (docs/requirements.md D56, D84). Graph cannot create received mail; IMAP
// APPEND can. Nothing else uses IMAP.
package outlookimap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
)

// DefaultAddress is Outlook.com's IMAP server.
const DefaultAddress = "outlook.office365.com:993"

// ErrSignIn means the server refused the credentials.
var ErrSignIn = errors.New("imap sign-in refused")

// ErrIMAPOff means the server took the credentials but the account does
// not allow IMAP: Outlook.com's "Let devices and apps use IMAP" setting is
// off, as it is by default (F66).
var ErrIMAPOff = errors.New("imap is turned off for the account")

// imapOff reports whether an error is Outlook.com refusing a signed-in
// session because the account does not allow IMAP.
func imapOff(err error) bool {
	var imapErr *imap.Error

	return errors.As(err, &imapErr) && strings.HasPrefix(imapErr.Text, "User is authenticated but not connected")
}

// appendLimit is the largest message Outlook.com takes over IMAP, as
// measured (F74): it refused 13,844,480 bytes and took 13,803,520.
const appendLimit = 13_803_520

// tooBig reports whether a refused APPEND was refused for the message's
// size. Outlook.com never sends the TOOBIG response code: for a message
// above appendLimit it answers "BAD Command Argument Error. 11", and from
// about 40 MB "NO Maximum size of appendable message has been exceeded"
// (F74).
func tooBig(imapErr *imap.Error, size int) bool {
	switch {
	case imapErr.Code == imap.ResponseCodeTooBig:
		return true
	case strings.Contains(imapErr.Text, "Maximum size of appendable message"):
		return true
	default:
		return imapErr.Type == imap.StatusResponseTypeBad && strings.HasPrefix(imapErr.Text, "Command Argument Error") &&
			size > appendLimit
	}
}

// throttledRE matches the throttling answer Microsoft documents for Office
// 365 IMAP, "Request is throttled. Suggested Backoff Time: 299961
// milliseconds" (F75).
var throttledRE = regexp.MustCompile(`(?i)throttled.*?suggested backoff time:\s*(\d+)\s*milliseconds`)

// Throttled reports whether the server refused a command for throttling,
// and how long it asks to wait (F75).
func Throttled(err error) (time.Duration, bool) {
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		return 0, false
	}

	m := throttledRE.FindStringSubmatch(imapErr.Text)
	if m == nil {
		return 0, false
	}

	ms, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return 0, false
	}

	return time.Duration(ms) * time.Millisecond, true
}

// ErrTooBig means the server refused a message as larger than it accepts.
var ErrTooBig = errors.New("message too big")

// Mailbox is a signed-in IMAP connection.
type Mailbox struct {
	client *imapclient.Client
	// folders maps lowercased folder names to the names the server uses.
	folders map[string]string
	// special maps special-use attributes (\Sent, \Drafts, \Archive) to
	// folder names.
	special map[imap.MailboxAttr]string
	delim   rune
	// stop ends closing the connection when the context ends.
	stop func() bool
}

// dialTimeout bounds connecting, so a network that blocks the port fails
// instead of hanging.
const dialTimeout = 30 * time.Second

// DialTLS opens a TLS connection to an IMAP server.
func DialTLS(ctx context.Context, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", address, err)
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: dialTimeout},
		Config:    &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", address, err)
	}

	return conn, nil
}

// Open signs in on a connection and reads the folder list. When ctx ends,
// the connection is closed, which ends any command waiting on it.
func Open(ctx context.Context, conn net.Conn, auth sasl.Client) (*Mailbox, error) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) //nolint:errcheck // closing to interrupt; the command reports.
	c := imapclient.New(conn, nil)

	fail := func(err error) (*Mailbox, error) {
		stop()
		_ = c.Close() //nolint:errcheck // the first error is the one to report.

		return nil, err
	}

	if err := c.WaitGreeting(); err != nil {
		return fail(fmt.Errorf("imap greeting: %w", err))
	}

	if err := c.Authenticate(auth); err != nil {
		if imapOff(err) {
			return fail(fmt.Errorf("%w: %w", ErrIMAPOff, err))
		}

		return fail(fmt.Errorf("%w: %w", ErrSignIn, err))
	}

	m := &Mailbox{client: c, folders: map[string]string{}, special: map[imap.MailboxAttr]string{}, delim: '/', stop: stop}
	if err := m.list(); err != nil {
		if imapOff(err) {
			return fail(fmt.Errorf("%w: %w", ErrIMAPOff, err))
		}

		return fail(err)
	}

	return m, nil
}

func (m *Mailbox) list() error {
	boxes, err := m.client.List("", "*", nil).Collect()
	if err != nil {
		return fmt.Errorf("list imap folders: %w", err)
	}

	for _, b := range boxes {
		m.folders[strings.ToLower(b.Mailbox)] = b.Mailbox
		if b.Delim != 0 {
			m.delim = b.Delim
		}

		for _, attr := range b.Attrs {
			if attr == imap.MailboxAttrSent || attr == imap.MailboxAttrDrafts || attr == imap.MailboxAttrArchive {
				m.special[attr] = b.Mailbox
			}
		}
	}

	return nil
}

// Close signs out.
func (m *Mailbox) Close() error {
	m.stop()

	if err := m.client.Logout().Wait(); err != nil {
		_ = m.client.Close() //nolint:errcheck // the logout error is the one to report.

		return fmt.Errorf("imap sign-out: %w", err)
	}

	if err := m.client.Close(); err != nil {
		return fmt.Errorf("close imap connection: %w", err)
	}

	return nil
}

// Special returns the folder with a special-use attribute, or fallback
// when the server marks none.
func (m *Mailbox) Special(attr imap.MailboxAttr, fallback string) string {
	if name, ok := m.special[attr]; ok {
		return name
	}

	return fallback
}

// Folder returns the server's name for a folder inside top (a folder the
// server has, such as "INBOX" or the \Sent folder; "" for the top level)
// given by its path of names, outermost first, creating it when missing.
// Names match whatever their case, as in Outlook.
func (m *Mailbox) Folder(top string, path []string) (string, error) {
	parts := path
	if top != "" {
		parts = append([]string{top}, path...)
	}

	name := strings.Join(parts, string(m.delim))
	if existing, ok := m.folders[strings.ToLower(name)]; ok {
		return existing, nil
	}

	if err := m.client.Create(name, nil).Wait(); err != nil {
		return "", fmt.Errorf("create imap folder %q: %w", name, err)
	}

	m.folders[strings.ToLower(name)] = name

	return name, nil
}

// Has reports whether the server lists a top-level folder of that name,
// whatever its case.
func (m *Mailbox) Has(name string) bool {
	_, ok := m.folders[strings.ToLower(name)]

	return ok
}

// Reserved reports whether the server refused to create a folder because
// it keeps the name for itself, such as Calendar or Tasks (F73).
func Reserved(err error) bool {
	var imapErr *imap.Error

	return errors.As(err, &imapErr) && strings.HasPrefix(imapErr.Text, "Folder name is reserved")
}

// Refused reports whether an error is the server's answer to a command, as
// opposed to a broken connection.
func Refused(err error) bool {
	var imapErr *imap.Error

	return errors.As(err, &imapErr)
}

// Append adds a message to a folder with flags and an arrival time. It
// returns ErrTooBig when the server refuses the message's size.
func (m *Mailbox) Append(folder string, raw []byte, flags []imap.Flag, arrived time.Time) error {
	cmd := m.client.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: arrived})

	if _, err := cmd.Write(raw); err != nil {
		_ = cmd.Close() //nolint:errcheck // the write error is the one to report.

		return fmt.Errorf("append to %s: %w", folder, err)
	}

	if err := cmd.Close(); err != nil {
		return fmt.Errorf("append to %s: %w", folder, err)
	}

	if _, err := cmd.Wait(); err != nil {
		var imapErr *imap.Error
		if errors.As(err, &imapErr) && tooBig(imapErr, len(raw)) {
			return fmt.Errorf("append to %s: %w: %w", folder, ErrTooBig, err)
		}

		return fmt.Errorf("append to %s: %w", folder, err)
	}

	return nil
}

// XOAuth2 is the XOAUTH2 SASL mechanism Outlook.com uses for OAuth.
type XOAuth2 struct {
	Username, AccessToken string
}

// Start implements sasl.Client.
func (x *XOAuth2) Start() (mech string, ir []byte, err error) {
	return "XOAUTH2", []byte("user=" + x.Username + "\x01auth=Bearer " + x.AccessToken + "\x01\x01"), nil
}

// Next implements sasl.Client: after a failure the server sends an error
// description, answered with an empty response to get the final status.
func (x *XOAuth2) Next(_ []byte) ([]byte, error) {
	return []byte{}, nil
}
