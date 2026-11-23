package gmail_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/gmail/gmailtest"
	"github.com/tonimelisma/pimvault/internal/googleapi"
)

func TestMessageIDs_FollowsPagesAndFiltersByLabel(t *testing.T) {
	srv := gmailtest.New(t)
	srv.PageSize = 2
	srv.Add("a1", "x", "INBOX")
	srv.Add("a2", "x", "SENT")
	srv.Add("a3", "x", "INBOX", "SENT")
	srv.Add("a4", "x")
	srv.Add("a5", "x", "TRASH")

	client := gmail.New(http.DefaultClient, srv.URL)

	all, err := client.MessageIDs(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2", "a3", "a4"}, all)
	assert.Equal(t, 2, srv.Requests["/messages"])

	sent, err := client.MessageIDs(t.Context(), "SENT")
	require.NoError(t, err)
	assert.Equal(t, []string{"a2", "a3"}, sent)
}

func TestHistory_CollectsChangedMessagesOnce(t *testing.T) {
	srv := gmailtest.New(t)
	client := gmail.New(http.DefaultClient, srv.URL)

	start, err := client.HistoryID(t.Context())
	require.NoError(t, err)

	srv.PageSize = 1
	srv.Add("m1", "x", "INBOX")
	srv.SetLabels("m1")
	srv.Add("m2", "x")
	srv.Delete("m2")

	changed, latest, err := client.History(t.Context(), start)
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2"}, changed)
	assert.NotEqual(t, start, latest)

	again, sameLatest, err := client.History(t.Context(), latest)
	require.NoError(t, err)
	assert.Empty(t, again)
	assert.Equal(t, latest, sameLatest)
}

func TestHistory_ExpiredStart(t *testing.T) {
	srv := gmailtest.New(t)
	client := gmail.New(http.DefaultClient, srv.URL)

	start, err := client.HistoryID(t.Context())
	require.NoError(t, err)

	srv.ExpireHistory()

	_, _, err = client.History(t.Context(), start)
	require.ErrorIs(t, err, gmail.ErrHistoryExpired)
}

func TestRawAndLabels(t *testing.T) {
	srv := gmailtest.New(t)
	srv.Add("m1", "Subject: hi\r\n\r\nbody?", "INBOX", "Label_1")
	client := gmail.New(http.DefaultClient, srv.URL)

	raw, err := client.Raw(t.Context(), "m1")
	require.NoError(t, err)
	assert.Equal(t, "Subject: hi\r\n\r\nbody?", string(raw))

	labels, err := client.MessageLabels(t.Context(), "m1")
	require.NoError(t, err)
	assert.Equal(t, []string{"INBOX", "Label_1"}, labels)

	_, err = client.Raw(t.Context(), "missing")
	require.ErrorIs(t, err, googleapi.ErrNotFound)

	_, err = client.MessageLabels(t.Context(), "missing")
	require.ErrorIs(t, err, googleapi.ErrNotFound)
}

func TestUserLabels(t *testing.T) {
	srv := gmailtest.New(t)
	srv.AddLabel("Label_1", "Work/Projects")
	client := gmail.New(http.DefaultClient, srv.URL)

	labels, err := client.UserLabels(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Label_1": "Work/Projects"}, labels)
}

func TestAPIError(t *testing.T) {
	srv := gmailtest.New(t)
	srv.Add("m1", "x")
	srv.FailRaw["m1"] = http.StatusBadRequest
	client := gmail.New(http.DefaultClient, srv.URL)

	_, err := client.Raw(t.Context(), "m1")

	var apiErr *googleapi.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Status)
	assert.Equal(t, "injected failure", apiErr.Message)
	assert.Contains(t, err.Error(), "get message m1")
}

func TestFolder(t *testing.T) {
	labels := map[string]string{"L1": "Work/Projects", "L2": "Alpha", "L3": "[Gmail]/All Mail", "L4": "Archive", "L5": "Inbox/Old"}

	cases := map[string]struct {
		labelIDs []string
		want     string
		ok       bool
	}{
		"inbox wins":            {[]string{"L1", "SENT", "INBOX", "DRAFT"}, "Inbox", true},
		"drafts before sent":    {[]string{"SENT", "DRAFT"}, "Drafts", true},
		"sent before labels":    {[]string{"L1", "SENT"}, "Sent", true},
		"first label by name":   {[]string{"L1", "L2", "IMPORTANT"}, "Alpha", true},
		"nested label":          {[]string{"L1"}, "Work/Projects", true},
		"brackets kept":         {[]string{"L3"}, "[Gmail]/All Mail", true},
		"archive":               {[]string{"IMPORTANT", "CATEGORY_UPDATES"}, "Archive", true},
		"no labels":             {nil, "Archive", true},
		"spam excluded":         {[]string{"SPAM", "INBOX"}, "", false},
		"trash excluded":        {[]string{"TRASH", "L1"}, "", false},
		"unknown label ignored": {[]string{"Label_99"}, "Archive", true},
		"label named Archive":   {[]string{"L4"}, "%41rchive", true},
		"label under Inbox":     {[]string{"L5"}, "%49nbox/Old", true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			folder, ok := gmail.Folder(tc.labelIDs, labels)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, filepath.FromSlash(tc.want), folder)
		})
	}
}
