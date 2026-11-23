package graph_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
)

func TestMailFolders(t *testing.T) {
	srv := graphtest.New(t)
	srv.PageSize = 3
	srv.AddFolder("u-work", "Work", "")
	srv.AddFolder("u-proj", "Projects", "u-work")
	srv.AddFolder("u-old", "Old", "f-inbox")
	srv.AddFolder("u-trash-sub", "Kept in trash", "f-deleted")
	srv.AddFolder("u-archive", "Archive", "u-work")
	srv.AddFolder("u-top-archive", "Sent", "")
	srv.AddFolder("u-slash", "a/../../b", "")

	folders, err := graph.New(http.DefaultClient, srv.URL).MailFolders(t.Context())
	require.NoError(t, err)

	got := make(map[string]string)
	for _, f := range folders {
		got[f.ID] = filepath.ToSlash(f.Path)
	}

	assert.Equal(t, map[string]string{
		"f-inbox":       "Inbox",
		"f-drafts":      "Drafts",
		"f-sent":        "Sent",
		"f-archive":     "Archive",
		"u-old":         "Inbox/Old",
		"u-work":        "Work",
		"u-proj":        "Work/Projects",
		"u-archive":     "Work/Archive",
		"u-top-archive": "%53ent",
		"u-slash":       "a%2F..%2F..%2Fb",
	}, got)
}

func TestMessageDelta(t *testing.T) {
	srv := graphtest.New(t)
	srv.PageSize = 2
	srv.Add("m1", "f-inbox", "one")
	srv.Add("m2", "f-inbox", "two")
	srv.Add("m3", "f-inbox", "three")

	client := graph.New(http.DefaultClient, srv.URL)

	first, err := client.MessageDelta(t.Context(), "f-inbox", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2", "m3"}, first.Added)
	assert.Empty(t, first.Removed)
	require.NotEmpty(t, first.Link)

	srv.Move("m1", "f-archive")
	srv.Delete("m2")
	srv.Add("m4", "f-inbox", "four")

	next, err := client.MessageDelta(t.Context(), "f-inbox", first.Link)
	require.NoError(t, err)
	assert.Equal(t, []string{"m4"}, next.Added)
	assert.Equal(t, []string{"m1", "m2"}, next.Removed)

	archived, err := client.MessageDelta(t.Context(), "f-archive", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"m1"}, archived.Added)
}

func TestMessageDelta_Expired(t *testing.T) {
	srv := graphtest.New(t)
	client := graph.New(http.DefaultClient, srv.URL)

	first, err := client.MessageDelta(t.Context(), "f-inbox", "")
	require.NoError(t, err)

	srv.ExpireDelta()

	_, err = client.MessageDelta(t.Context(), "f-inbox", first.Link)
	require.ErrorIs(t, err, graph.ErrDeltaExpired)
}

func TestRaw(t *testing.T) {
	srv := graphtest.New(t)
	srv.Add("m1", "f-sent", "Subject: hi\r\n\r\nbody")
	client := graph.New(http.DefaultClient, srv.URL)

	raw, err := client.Raw(t.Context(), "m1")
	require.NoError(t, err)
	assert.Equal(t, "Subject: hi\r\n\r\nbody", string(raw))

	_, err = client.Raw(t.Context(), "missing")
	require.ErrorIs(t, err, graph.ErrNotFound)

	srv.FailRaw["m1"] = http.StatusBadRequest
	_, err = client.Raw(t.Context(), "m1")

	var apiErr *graph.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "TestInjected", apiErr.Code)
}

func TestLinksToOtherHostsAreRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the token must not be sent to another host")
	}))
	t.Cleanup(other.Close)

	srv := graphtest.New(t)
	client := graph.New(http.DefaultClient, srv.URL)

	_, err := client.MessageDelta(t.Context(), "f-inbox", other.URL+"/delta?$deltatoken=1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the Graph host")
}

func TestMailboxTimeZone(t *testing.T) {
	for body, want := range map[string]string{
		`{"timeZone":"Europe/Helsinki","workingHours":{"timeZone":{"name":"Pacific Standard Time"}}}`: "Europe/Helsinki",
		`{"workingHours":{"timeZone":{"name":"Pacific Standard Time"}}}`:                              "Pacific Standard Time",
		`{}`: "",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/mailboxSettings", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(body))
			assert.NoError(t, err)
		}))

		got, err := graph.New(http.DefaultClient, srv.URL).MailboxTimeZone(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, got, body)
		srv.Close()
	}
}

func TestNewContactFingerprint(t *testing.T) {
	written := graph.NewContact{
		DisplayName: "Ann", Birthday: "1996-11-16T11:59:00Z", PersonalNotes: "a\nb",
		EmailAddresses: []graph.EmailAddress{{Address: "ann@example.com"}},
	}
	written.SetSource("people/c1")

	// As Outlook returns it: no archive ID, the address repeated as the
	// name, other line endings, empty addresses.
	read := graph.NewContact{
		DisplayName: "Ann", Birthday: "1996-11-16T00:00:00Z", PersonalNotes: "a\r\nb",
		EmailAddresses: []graph.EmailAddress{{Name: "ann@example.com", Address: "ann@example.com"}},
		HomeAddress:    &graph.PhysicalAddress{},
	}
	assert.Equal(t, written.Fingerprint(), read.Fingerprint())

	read.MobilePhone = "+358 40 1"
	assert.NotEqual(t, written.Fingerprint(), read.Fingerprint())
}

func TestAllContacts_AccountWithoutTheFolderHasNone(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, err := w.Write([]byte(`{"error":{"code":"ErrorInvalidRequest","message":"no"}}`))
			assert.NoError(t, err)
		}))

		got, err := graph.New(http.DefaultClient, srv.URL).AllContacts(t.Context())
		require.NoError(t, err, status)
		assert.Empty(t, got)
		srv.Close()
	}
}

func TestRestoreFolders(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddFolder("u-work", "Work", "")
	srv.AddFolder("u-trash-sub", "Kept in trash", "f-deleted")
	srv.AddFolder("u-inbox-sub", "Sub", "f-inbox")

	folders, err := graph.New(http.DefaultClient, srv.URL).RestoreFolders(t.Context())
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{"f-deleted": true, "u-trash-sub": true, "f-junk": true, "f-outbox": true, "f-history": true},
		folders.Skipped)
	assert.Equal(t, map[string]bool{"work": true}, folders.User)
	assert.Equal(t, map[string]bool{
		"inbox": true, "drafts": true, "sent items": true, "deleted items": true, "junk email": true, "archive": true,
		"outbox": true, "conversation history": true,
	}, folders.System)
}
