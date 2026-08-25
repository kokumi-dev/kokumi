package scmlink

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRepo   = "https://github.com/kokumi-dev/example"
	testCommit = "abcdef1234567890abcdef1234567890abcdef12"
	testTag    = "1.2.3"
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
			repo:      testRepo,
			commit:    testCommit,
			wantURL:   "https://github.com/kokumi-dev/example/commit/abcdef1234567890abcdef1234567890abcdef12",
			wantLabel: "abcdef1",
		},
		{
			name:      "tag preferred over commit hash",
			repo:      testRepo,
			tag:       testTag,
			commit:    testCommit,
			wantURL:   "https://github.com/kokumi-dev/example/tree/1.2.3",
			wantLabel: testTag,
		},
		{
			name:      "tag only, no commit",
			repo:      testRepo,
			tag:       testTag,
			wantURL:   "https://github.com/kokumi-dev/example/tree/1.2.3",
			wantLabel: testTag,
		},
		{
			name:      "commit only when no tag",
			repo:      testRepo,
			commit:    testCommit,
			wantURL:   "https://github.com/kokumi-dev/example/commit/abcdef1234567890abcdef1234567890abcdef12",
			wantLabel: "abcdef1",
		},
		{
			name:    "missing ref returns nil",
			repo:    testRepo,
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
