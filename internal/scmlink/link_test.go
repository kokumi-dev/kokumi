package scmlink

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name      string
		repo      string
		tag       string
		commit    string
		wantURL   string
		wantLabel string
		wantNil   bool
	}{
		{
			name:      "github https url with commit sha",
			repo:      "https://github.com/kokumi-dev/example",
			commit:    "abcdef1234567890abcdef1234567890abcdef12",
			wantURL:   "https://github.com/kokumi-dev/example/commit/abcdef1234567890abcdef1234567890abcdef12",
			wantLabel: "abcdef1",
		},
		{
			name:      "tag preferred over commit hash",
			repo:      "https://github.com/kokumi-dev/example",
			tag:       "1.2.3",
			commit:    "abcdef1234567890abcdef1234567890abcdef12",
			wantURL:   "https://github.com/kokumi-dev/example/tree/1.2.3",
			wantLabel: "1.2.3",
		},
		{
			name:      "tag only, no commit",
			repo:      "https://github.com/kokumi-dev/example",
			tag:       "1.2.3",
			wantURL:   "https://github.com/kokumi-dev/example/tree/1.2.3",
			wantLabel: "1.2.3",
		},
		{
			name:      "commit only when no tag",
			repo:      "https://github.com/kokumi-dev/example",
			commit:    "abcdef1234567890abcdef1234567890abcdef12",
			wantURL:   "https://github.com/kokumi-dev/example/commit/abcdef1234567890abcdef1234567890abcdef12",
			wantLabel: "abcdef1",
		},
		{
			name:    "missing ref returns nil",
			repo:    "https://github.com/kokumi-dev/example",
			wantNil: true,
		},
		{
			name:    "missing repo returns nil",
			commit:  "abc123",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(tc.repo, tc.tag, tc.commit)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.wantURL, got.URL)
			assert.Equal(t, tc.wantLabel, got.Label)
		})
	}
}
