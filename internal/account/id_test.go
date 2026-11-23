package account

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    ID
		wantErr string
	}{
		{in: "gmail:user@gmail.example.com", want: ID{Provider: Gmail, Email: "user@gmail.example.com"}},
		{in: "outlook:user@gmail.example.com", want: ID{Provider: Outlook, Email: "user@gmail.example.com"}},
		{in: "gmail:first_last@example.com", want: ID{Provider: Gmail, Email: "first_last@example.com"}},
		{in: "gmail:User@Gmail.Example.com", want: ID{Provider: Gmail, Email: "user@gmail.example.com"}},
		{in: "user@gmail.example.com", wantErr: "want provider:email"},
		{in: "yahoo:user@yahoo.example.com", wantErr: "unknown provider"},
		{in: "gmail:", wantErr: "invalid email"},
		{in: "gmail:no-at-sign", wantErr: "invalid email"},
		{in: "gmail:a@b@c", wantErr: "invalid email"},
		{in: "gmail:../../etc@x", wantErr: "invalid email"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.in)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, strings.ToLower(tt.in), got.String())
		})
	}
}

func TestDirNameRoundTrip(t *testing.T) {
	t.Parallel()

	id := ID{Provider: Gmail, Email: "first_last@example.com"}
	assert.Equal(t, "gmail_first_last@example.com", id.DirName())

	back, err := ParseDirName(id.DirName())
	require.NoError(t, err)
	assert.Equal(t, id, back)
}

func TestMatch(t *testing.T) {
	t.Parallel()

	gmailToni := ID{Provider: Gmail, Email: "user@gmail.example.com"}
	outlookToni := ID{Provider: Outlook, Email: "user@outlook.example.com"}
	outlookWork := ID{Provider: Outlook, Email: "work@example.com"}
	ids := []ID{gmailToni, outlookToni, outlookWork}

	tests := []struct {
		query   string
		want    ID
		wantErr error
	}{
		{query: "gmail:user@gmail.example.com", want: gmailToni},
		{query: "GMAIL:User@Gmail.Example.com", want: gmailToni},
		{query: "gmail", want: gmailToni},
		{query: "outlook:user", want: outlookToni},
		{query: "work", want: outlookWork},
		{query: "outlook", wantErr: ErrAmbiguous},
		{query: "user", wantErr: ErrAmbiguous},
		{query: "nobody", wantErr: ErrNoMatch},
		{query: "", wantErr: ErrNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			got, err := Match(ids, tt.query)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_AmbiguousErrorListsCandidates(t *testing.T) {
	t.Parallel()

	_, err := Match([]ID{
		{Provider: Outlook, Email: "b@example.com"},
		{Provider: Outlook, Email: "a@example.com"},
	}, "outlook")
	require.ErrorIs(t, err, ErrAmbiguous)
	assert.Contains(t, err.Error(), "outlook:a@example.com, outlook:b@example.com")
}
