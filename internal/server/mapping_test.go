package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testEmail       = "alice@example.com"
	testGroup       = "devops"
	testGroup2      = "leads"
	testEmailClaim  = "email"
	testSubClaim    = "sub"
	testGroupsClaim = "groups"
	testRealm       = "realm_access"
	testRoles       = "roles"
)

func TestMatchesIdentity(t *testing.T) {
	oidc := &Identity{Subject: "123456", Email: testEmail, Groups: []string{testGroup, testGroup2}, Provider: providerOIDC}

	saWith := func(ann map[string]string) *corev1.ServiceAccount {
		meta := metav1.ObjectMeta{Name: "test", Annotations: ann}
		return &corev1.ServiceAccount{ObjectMeta: meta}
	}

	tests := []struct {
		name string
		ann  map[string]string
		id   *Identity
		want bool
	}{
		{
			name: "no annotations never match",
			ann:  nil,
			id:   oidc,
			want: false,
		},
		{
			name: "sub match",
			ann:  map[string]string{annotationIdentitySub: "123456"},
			id:   oidc,
			want: true,
		},
		{
			name: "sub mismatch",
			ann:  map[string]string{annotationIdentitySub: "other"},
			id:   oidc,
			want: false,
		},
		{
			name: "email match",
			ann:  map[string]string{annotationIdentityEmail: testEmail},
			id:   oidc,
			want: true,
		},
		{
			name: "groups match one of many",
			ann:  map[string]string{annotationIdentityGroups: "platform, leads"},
			id:   oidc,
			want: true,
		},
		{
			name: "groups mismatch",
			ann:  map[string]string{annotationIdentityGroups: "platform, sre"},
			id:   oidc,
			want: false,
		},
		{
			name: "groups with whitespace",
			ann:  map[string]string{annotationIdentityGroups: " devops "},
			id:   oidc,
			want: true,
		},
		{
			name: "nil identity never matches",
			ann:  map[string]string{annotationIdentitySub: "123456"},
			id:   nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchesIdentity(saWith(tt.ann), tt.id))
		})
	}
}

func TestResolveServiceAccounts(t *testing.T) {
	adminMeta := metav1.ObjectMeta{Name: adminServiceAccountName}
	admin := &corev1.ServiceAccount{ObjectMeta: adminMeta}
	editorMeta := metav1.ObjectMeta{Name: "kokumi-editor", Annotations: map[string]string{
		annotationIdentityGroups: testGroup,
	}}
	editor := &corev1.ServiceAccount{ObjectMeta: editorMeta}
	viewerMeta := metav1.ObjectMeta{Name: "kokumi-viewer", Annotations: map[string]string{
		annotationIdentityEmail: "bob@example.com",
	}}
	viewer := &corev1.ServiceAccount{ObjectMeta: viewerMeta}
	sas := []*corev1.ServiceAccount{admin, editor, viewer}

	// Mapping OIDC users to the admin SA is a deliberate admin decision
	// expressed via annotations; it works like any other SA.
	adminWithAnn := admin.DeepCopy()
	adminWithAnn.Annotations = map[string]string{annotationIdentityEmail: "mallory@example.com"}

	tests := []struct {
		name string
		sas  []*corev1.ServiceAccount
		id   *Identity
		want []string // expected SA names, in order
	}{
		{
			name: "admin login maps to the admin SA only",
			sas:  sas,
			id:   &Identity{Subject: "admin", Provider: providerAdmin},
			want: []string{adminServiceAccountName},
		},
		{
			name: "oidc group maps to editor",
			sas:  sas,
			id:   &Identity{Subject: "123456", Groups: []string{testGroup}, Provider: providerOIDC},
			want: []string{"kokumi-editor"},
		},
		{
			name: "unmapped oidc user gets nothing",
			sas:  sas,
			id:   &Identity{Subject: "nobody", Email: "nobody@example.com", Provider: providerOIDC},
			want: nil,
		},
		{
			name: "oidc user can be mapped to the admin SA via annotations",
			sas:  []*corev1.ServiceAccount{adminWithAnn},
			id:   &Identity{Subject: "mallory", Email: "mallory@example.com", Provider: providerOIDC},
			want: []string{adminServiceAccountName},
		},
		{
			name: "nil identity resolves to nothing",
			sas:  sas,
			id:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := resolveServiceAccounts(tt.sas, tt.id)
			got := make([]string, 0, len(out))
			for _, sa := range out {
				got = append(got, sa.Name)
			}
			if tt.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIdentityFromClaims(t *testing.T) {
	tests := []struct {
		name          string
		claims        map[string]any
		usernameClaim string
		emailClaim    string
		groupsClaim   string
		want          *Identity
		wantErr       bool
	}{
		{
			name: "full claims",
			claims: map[string]any{
				testSubClaim:   "123456",
				testEmailClaim: testEmail,
				"groups":       []any{testGroup, testGroup2},
			},
			usernameClaim: testEmailClaim,
			emailClaim:    testEmailClaim,
			groupsClaim:   testGroupsClaim,
			want: &Identity{
				Subject:  "123456",
				Email:    testEmail,
				Groups:   []string{testGroup, testGroup2},
				Provider: providerOIDC,
			},
		},
		{
			name:          "missing sub falls back to username claim",
			claims:        map[string]any{testEmailClaim: testEmail},
			usernameClaim: testEmailClaim,
			emailClaim:    testEmailClaim,
			groupsClaim:   testGroupsClaim,
			want: &Identity{
				Subject:  testEmail,
				Email:    testEmail,
				Provider: providerOIDC,
			},
		},
		{
			name:          "no usable claim errors",
			claims:        map[string]any{},
			usernameClaim: testEmailClaim,
			emailClaim:    testEmailClaim,
			groupsClaim:   testGroupsClaim,
			wantErr:       true,
		},
		{
			name: "custom email and groups claims are honored",
			// Keycloak-style token: email under "user.email", groups under
			// "realm_access.roles".
			claims: map[string]any{
				testSubClaim: "123456",
				"user": map[string]any{
					testEmailClaim: testEmail,
				},
				testRealm: map[string]any{
					testRoles: []any{testGroup},
				},
			},
			usernameClaim: testEmailClaim,
			emailClaim:    "user.email",
			groupsClaim:   "realm_access.roles",
			want: &Identity{
				Subject:  "123456",
				Email:    testEmail,
				Groups:   []string{testGroup},
				Provider: providerOIDC,
			},
		},
		{
			name: "default claims ignore nonstandard locations",
			claims: map[string]any{
				testSubClaim: "123456",
				testRealm:    map[string]any{testRoles: []any{testGroup}},
			},
			usernameClaim: testEmailClaim,
			emailClaim:    testEmailClaim,
			groupsClaim:   testGroupsClaim,
			want: &Identity{
				Subject:  "123456",
				Provider: providerOIDC,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := identityFromClaims(tt.claims, tt.usernameClaim, tt.emailClaim, tt.groupsClaim)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, id)
		})
	}
}
