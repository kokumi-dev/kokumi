package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClaimAtPath(t *testing.T) {
	const (
		namespacedGroups = "https://kokumi.example.com/groups"
		realmRoles       = "realm_access.roles"
	)
	tests := []struct {
		name   string
		claims map[string]any
		path   string
		want   any
		wantOK bool
	}{
		{
			name:   "top-level name with dots",
			claims: map[string]any{namespacedGroups: []any{testGroup}},
			path:   namespacedGroups,
			want:   []any{testGroup},
			wantOK: true,
		},
		{
			name:   "dotted path into nested claims",
			claims: map[string]any{testRealm: map[string]any{testRoles: []any{testGroup}}},
			path:   realmRoles,
			want:   []any{testGroup},
			wantOK: true,
		},
		{
			name: "top-level name wins over nested path",
			claims: map[string]any{
				realmRoles: []any{"top-level"},
				testRealm:  map[string]any{testRoles: []any{"nested"}},
			},
			path:   realmRoles,
			want:   []any{"top-level"},
			wantOK: true,
		},
		{
			name:   "missing claim",
			claims: map[string]any{},
			path:   namespacedGroups,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := claimAtPath(tt.claims, tt.path)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
