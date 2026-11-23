// Package outlookimaptest is an IMAP server for tests: go-imap's in-memory
// server with Outlook.com's folders, XOAUTH2 sign-in and a size cap.
package outlookimaptest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
)

// Appended is a message added through APPEND.
type Appended struct {
	Folder string
	Raw    string
	Flags  []imap.Flag
	Time   time.Time
}

// Server is a fake Outlook.com IMAP server.
type Server struct {
	// Address is where the server listens, without TLS.
	Address string
	// MaxSize refuses larger messages as Outlook.com does (F74): "BAD
	// Command Argument Error. 11", or above three times MaxSize "NO Maximum
	// size of appendable message has been exceeded". 0 means no cap.
	MaxSize int64
	// DropAt ends the connection, as Outlook.com does when a session's
	// token expires, instead of storing the message with this subject.
	DropAt string

	mu       sync.Mutex
	appended []Appended
	throttle int
	imapOff  bool
}

// SetIMAPOff makes the server refuse signed-in sessions, as Outlook.com
// does while the account's IMAP setting is off.
func (s *Server) SetIMAPOff(off bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.imapOff = off
}

// New starts a server for one account whose access token is token; it is
// closed when the test ends.
func New(t *testing.T, email, token string) *Server {
	t.Helper()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(email, token)

	// Outlook.com's own folders as IMAP lists them; Notes is hidden from
	// Graph (F73).
	for _, name := range []string{"INBOX", "Sent", "Drafts", "Archive", "Deleted Items", "Junk Email", "Outbox", "Notes"} {
		if err := user.Create(name, nil); err != nil {
			t.Fatal(err)
		}
	}

	mem.AddUser(user)

	s := &Server{}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &session{Session: mem.NewSession(), server: s, conn: conn}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
		Logger:       log.New(io.Discard, "", 0),
	})

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = srv.Serve(ln) }() //nolint:errcheck // Serve ends with an error when the test closes the server.

	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck // nothing to do about a failed close in a test.

	s.Address = ln.Addr().String()

	return s
}

// Throttle makes the next n APPENDs fail with the throttling answer
// Microsoft documents for Office 365 (F75).
func (s *Server) Throttle(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.throttle = n
}

// Appended returns the messages added through APPEND, in order.
func (s *Server) Appended() []Appended {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Appended(nil), s.appended...)
}

// session adds XOAUTH2 and the size cap to an in-memory session.
type session struct {
	imapserver.Session

	server *Server
	conn   *imapserver.Conn
}

func (s *session) AuthenticateMechanisms() []string { return []string{"XOAUTH2"} }

func (s *session) Authenticate(mech string) (sasl.Server, error) {
	if mech != "XOAUTH2" {
		return nil, errors.New("unsupported mechanism")
	}

	return xoauth2{login: func(username, password string) error {
		if err := s.Login(username, password); err != nil {
			return err //nolint:wrapcheck // the memory server's refusal, as is.
		}

		s.server.mu.Lock()
		off := s.server.imapOff
		s.server.mu.Unlock()

		if off {
			return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "User is authenticated but not connected."}
		}

		return nil
	}}, nil
}

// reserved are the names Outlook.com refuses to create a folder by (F73).
func reserved() []string {
	return []string{"calendar", "contacts", "files", "tasks", "junk email", "deleted items"}
}

func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	top, _, _ := strings.Cut(mailbox, "/")
	if slices.Contains(reserved(), strings.ToLower(top)) {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Folder name is reserved."}
	}

	return s.Session.Create(mailbox, options) //nolint:wrapcheck // the memory server's answer, as is.
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	s.server.mu.Lock()
	drop := s.server.DropAt != "" && strings.Contains(string(raw), "Subject: "+s.server.DropAt+"\r\n")
	if drop {
		s.server.DropAt = ""
	}
	s.server.mu.Unlock()

	if drop {
		_ = s.conn.NetConn().Close() //nolint:errcheck // dropping the connection is the point.

		return nil, errors.New("connection dropped")
	}

	s.server.mu.Lock()
	throttled := s.server.throttle > 0
	if throttled {
		s.server.throttle--
	}
	s.server.mu.Unlock()

	if throttled {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Request is throttled. Suggested Backoff Time: 299961 milliseconds"}
	}

	if size := int64(len(raw)); s.server.MaxSize > 0 && size > 3*s.server.MaxSize {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Maximum size of appendable message has been exceeded"}
	} else if s.server.MaxSize > 0 && size > s.server.MaxSize {
		return nil, &imap.Error{Type: imap.StatusResponseTypeBad, Text: "Command Argument Error. 11"}
	}

	data, err := s.Session.Append(mailbox, literal{bytes.NewReader(raw)}, options)
	if err != nil {
		return nil, fmt.Errorf("append: %w", err)
	}

	s.server.mu.Lock()
	s.server.appended = append(s.server.appended, Appended{Folder: mailbox, Raw: string(raw), Flags: options.Flags, Time: options.Time})
	s.server.mu.Unlock()

	return data, nil
}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return l.Reader.Size() }

// xoauth2 checks an XOAUTH2 initial response: the token is the password.
type xoauth2 struct {
	login func(username, password string) error
}

func (x xoauth2) Next(response []byte) ([]byte, bool, error) {
	var user, token string

	for part := range strings.SplitSeq(string(response), "\x01") {
		if v, ok := strings.CutPrefix(part, "user="); ok {
			user = v
		}

		if v, ok := strings.CutPrefix(part, "auth=Bearer "); ok {
			token = v
		}
	}

	if err := x.login(user, token); err != nil {
		return nil, true, err
	}

	return nil, true, nil
}
