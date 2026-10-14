package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	capsInstallNamespace = "kokumi"
	kitchensResource     = "kitchens"
)

// rbacFunc decides a SelfSubjectAccessReview in place of the API server.
type rbacFunc func(ra authorizationv1.ResourceAttributes) (bool, error)

// adminRBAC mirrors server-role: everything, including kitchens.
func adminRBAC(authorizationv1.ResourceAttributes) (bool, error) { return true, nil }

// editorRBAC mirrors server-editor-role: everything except kitchens.
func editorRBAC(ra authorizationv1.ResourceAttributes) (bool, error) {
	return ra.Resource != kitchensResource, nil
}

// viewerRBAC mirrors server-viewer-role: read-only.
func viewerRBAC(ra authorizationv1.ResourceAttributes) (bool, error) {
	return slices.Contains([]string{"get", "list", "watch"}, ra.Verb), nil
}

// unimpersonableRBAC fails every check, like a ServiceAccount kokumi-server may
// not impersonate.
func unimpersonableRBAC(authorizationv1.ResourceAttributes) (bool, error) {
	return false, errors.New("failed to get server groups: unknown")
}

// allowOnly allows exactly the given verb on the given resource.
func allowOnly(verb, resource string) rbacFunc {
	return func(ra authorizationv1.ResourceAttributes) (bool, error) {
		return ra.Verb == verb && ra.Resource == resource, nil
	}
}

// ssarClient is a fake client answering SelfSubjectAccessReviews with rbac.
func ssarClient(t *testing.T, rbac rbacFunc) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(unionScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			review, ok := obj.(*authorizationv1.SelfSubjectAccessReview)
			if !ok {
				return errors.New("unexpected create")
			}
			allowed, err := rbac(*review.Spec.ResourceAttributes)
			review.Status.Allowed = allowed
			return err
		},
	}).Build()
}

// capsUserClient maps the user to one ServiceAccount per rbac function.
func capsUserClient(t *testing.T, rbacs ...rbacFunc) *userClient {
	t.Helper()
	imp := newImpersonator(nil, nil, capsInstallNamespace)
	uc := &userClient{imp: imp}
	for i, rbac := range rbacs {
		name := string(rune('a'+i)) + "-sa"
		imp.clients[name] = ssarClient(t, rbac)
		uc.sas = append(uc.sas, newSA(name))
	}
	return uc
}

var (
	adminLogin    = &Identity{Subject: "admin", Provider: providerAdmin}
	personalLogin = &Identity{Subject: "alice", Provider: providerOIDC, Issuer: "https://idp.example.com"}
)

// allCapabilities lists every capability in the catalog, sorted.
func allCapabilities() []Capability {
	rules := capabilityCatalog(capsInstallNamespace)
	all := make([]Capability, 0, len(rules))
	for _, rule := range rules {
		all = append(all, rule.capability)
	}
	slices.Sort(all)
	return all
}

// without returns caps minus the excluded capabilities.
func without(caps []Capability, excluded ...Capability) []Capability {
	return slices.DeleteFunc(slices.Clone(caps), func(c Capability) bool {
		return slices.Contains(excluded, c)
	})
}

func TestCapabilities(t *testing.T) {
	all := allCapabilities()

	tests := []struct {
		name  string
		id    *Identity
		rbacs []rbacFunc
		want  []Capability
		// wantUndetermined, when set, also expects an error.
		wantUndetermined []Capability
	}{
		{
			name:  "personal login with admin role gets everything",
			id:    personalLogin,
			rbacs: []rbacFunc{adminRBAC},
			want:  all,
		},
		{
			name:  "admin login cannot approve",
			id:    adminLogin,
			rbacs: []rbacFunc{adminRBAC},
			want:  without(all, CapPreparationsApprove),
		},
		{
			name:  "editor role gets everything but settings",
			id:    personalLogin,
			rbacs: []rbacFunc{editorRBAC},
			want:  without(all, CapSettingsGeneralUpdate, CapSettingsAuthUpdate),
		},
		{
			name:  "viewer role gets nothing",
			id:    personalLogin,
			rbacs: []rbacFunc{viewerRBAC},
			want:  []Capability{},
		},
		{
			name:  "no mapped ServiceAccount gets nothing",
			id:    personalLogin,
			rbacs: nil,
			want:  []Capability{},
		},
		{
			name:  "checks of one capability may pass on different ServiceAccounts",
			id:    personalLogin,
			rbacs: []rbacFunc{allowOnly("create", "servings"), allowOnly("update", "servings")},
			want:  []Capability{CapPreparationsPromote},
		},
		{
			name:  "promote needs both create and update on servings",
			id:    personalLogin,
			rbacs: []rbacFunc{allowOnly("update", "servings")},
			want:  []Capability{},
		},
		{
			name:  "pantry update maps to patch",
			id:    personalLogin,
			rbacs: []rbacFunc{allowOnly("patch", "pantries")},
			want:  []Capability{CapPantriesUpdate},
		},
		{
			name: "namespace-scoped grants do not count",
			id:   personalLogin,
			rbacs: []rbacFunc{func(ra authorizationv1.ResourceAttributes) (bool, error) {
				return ra.Namespace == "dev", nil
			}},
			want: []Capability{},
		},
		{
			name: "settings check targets the Kitchen singleton",
			id:   personalLogin,
			rbacs: []rbacFunc{func(ra authorizationv1.ResourceAttributes) (bool, error) {
				return ra.Resource == kitchensResource && ra.Namespace == capsInstallNamespace && ra.Name == "default", nil
			}},
			want: []Capability{CapSettingsAuthUpdate, CapSettingsGeneralUpdate},
		},
		{
			name: "failed checks are left out and reported",
			id:   personalLogin,
			rbacs: []rbacFunc{func(ra authorizationv1.ResourceAttributes) (bool, error) {
				if ra.Resource == kitchensResource {
					return false, errors.New("api server unavailable")
				}
				return true, nil
			}},
			want:             without(all, CapSettingsGeneralUpdate, CapSettingsAuthUpdate),
			wantUndetermined: []Capability{CapSettingsAuthUpdate, CapSettingsGeneralUpdate},
		},
		{
			name:  "a failing ServiceAccount does not mask a working one",
			id:    personalLogin,
			rbacs: []rbacFunc{unimpersonableRBAC, editorRBAC},
			// Only the settings stay undetermined: the editor is denied them,
			// and the failing ServiceAccount might have allowed them.
			want:             without(all, CapSettingsGeneralUpdate, CapSettingsAuthUpdate),
			wantUndetermined: []Capability{CapSettingsAuthUpdate, CapSettingsGeneralUpdate},
		},
		{
			name:             "only failing ServiceAccounts leave everything undetermined",
			id:               personalLogin,
			rbacs:            []rbacFunc{unimpersonableRBAC},
			want:             []Capability{},
			wantUndetermined: all,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := capsUserClient(t, tt.rbacs...)
			got, undetermined, err := uc.capabilities(context.Background(), tt.id, capabilityCatalog(capsInstallNamespace))
			if tt.wantUndetermined != nil {
				require.Error(t, err)
				assert.Equal(t, tt.wantUndetermined, undetermined)
			} else {
				require.NoError(t, err)
				assert.Empty(t, undetermined)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCapabilityCatalogUnique(t *testing.T) {
	all := allCapabilities()
	assert.Equal(t, slices.Compact(slices.Clone(all)), all, "every capability is defined once")
}

func TestHandleWhoami(t *testing.T) {
	adminSA := &corev1.ServiceAccount{Name: adminServiceAccountName}
	imp := newImpersonator(nil, nil, capsInstallNamespace)
	imp.clients[adminServiceAccountName] = ssarClient(t, adminRBAC)
	deps := &apiDeps{
		logger:       logr.Discard(),
		impersonator: imp,
		saList:       func() []*corev1.ServiceAccount { return []*corev1.ServiceAccount{adminSA} },
	}

	tests := []struct {
		name string
		id   *Identity
		want []Capability
	}{
		{name: "auth disabled", id: nil, want: []Capability{}},
		{name: "unmapped identity", id: personalLogin, want: []Capability{}},
		{name: "admin login", id: adminLogin, want: without(allCapabilities(), CapPreparationsApprove)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
			if tt.id != nil {
				req = req.WithContext(withIdentity(req.Context(), tt.id))
			}
			rec := httptest.NewRecorder()
			handleWhoami(deps, capsInstallNamespace)(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

			var body struct {
				Capabilities *[]Capability `json:"capabilities"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.NotNil(t, body.Capabilities, "capabilities is never null")
			assert.Equal(t, tt.want, *body.Capabilities)
		})
	}
}

func TestHandleWhoamiUndetermined(t *testing.T) {
	adminSA := &corev1.ServiceAccount{Name: adminServiceAccountName}
	imp := newImpersonator(nil, nil, capsInstallNamespace)
	imp.clients[adminServiceAccountName] = ssarClient(t, func(ra authorizationv1.ResourceAttributes) (bool, error) {
		if ra.Resource == kitchensResource {
			return false, errors.New("api server unavailable")
		}
		return true, nil
	})
	deps := &apiDeps{
		logger:       logr.Discard(),
		impersonator: imp,
		saList:       func() []*corev1.ServiceAccount { return []*corev1.ServiceAccount{adminSA} },
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req = req.WithContext(withIdentity(req.Context(), adminLogin))
	rec := httptest.NewRecorder()
	handleWhoami(deps, capsInstallNamespace)(rec, req)

	// Failed checks do not fail the request: the known capabilities are
	// reported, and the failed ones are named so the UI can keep its answer.
	require.Equal(t, http.StatusOK, rec.Code)
	var body whoamiResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, without(allCapabilities(), CapPreparationsApprove, CapSettingsGeneralUpdate, CapSettingsAuthUpdate), body.Capabilities)
	assert.Equal(t, []Capability{CapSettingsAuthUpdate, CapSettingsGeneralUpdate}, body.Undetermined)
}

func TestFirstAllowedSkipsFailingServiceAccounts(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		rbacs    []rbacFunc
		wantSA   string
		wantErr  bool
		wantDeny bool
	}{
		{name: "failing then allowed", rbacs: []rbacFunc{unimpersonableRBAC, adminRBAC}, wantSA: "b-sa"},
		{name: "failing then denied", rbacs: []rbacFunc{unimpersonableRBAC, viewerRBAC}, wantErr: true},
		{name: "all denied", rbacs: []rbacFunc{viewerRBAC}, wantDeny: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := capsUserClient(t, tt.rbacs...)

			sa, err := uc.imp.pickWriteSA(ctx, uc.sas, "create", "orders", "default")
			allowed, authErr := uc.authorized(ctx, "create", "orders", "default", "")
			reader, readerErr := uc.readerFor(ctx, "create", "orders", "default")

			switch {
			case tt.wantSA != "":
				require.NoError(t, err)
				assert.Equal(t, tt.wantSA, sa)
				require.NoError(t, authErr)
				assert.True(t, allowed)
				require.NoError(t, readerErr)
				assert.NotNil(t, reader)
			case tt.wantErr:
				// The failure is reported, not hidden behind a denial.
				for _, e := range []error{err, authErr, readerErr} {
					require.Error(t, e)
					assert.NotErrorIs(t, e, errNotAuthorized)
				}
				assert.False(t, allowed)
			case tt.wantDeny:
				require.ErrorIs(t, err, errNotAuthorized)
				require.NoError(t, authErr)
				assert.False(t, allowed)
				require.ErrorIs(t, readerErr, errNotAuthorized)
			}
		})
	}
}
