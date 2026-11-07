package server

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resources"
)

// newTestEndedSessions returns a record of ended sessions whose ConfigMap
// exists in a fake API server, and the fake's client.
func newTestEndedSessions(t *testing.T) (*endedSessions, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().WithObjects(resources.EndedSessionsConfigMap(testNS)).Build()
	return newEndedSessions(c, testNS), c
}

// storedEntries returns the data of the record's ConfigMap.
func storedEntries(t *testing.T, c client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: deliveryv1alpha1.EndedSessionsConfigMapName}, cm))
	return cm
}

func TestEndedSessions(t *testing.T) {
	const issuer, subject = "https://issuer.example", "user-1"
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	oidcSession := func(sid, providerSID string, signedIn time.Time) *verifiedToken {
		return &verifiedToken{
			identity: &Identity{Subject: subject, Provider: providerOIDC, Issuer: issuer},
			session:  sessionState{ID: sid, AuthTime: signedIn, ProviderSessionID: providerSID},
		}
	}

	t.Run("a sign-out ends its session and no other", func(t *testing.T) {
		ended, c := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedSessionKey("kokumi-1"), now))
		assert.True(t, ended.ended(oidcSession("kokumi-1", "", now)))
		assert.False(t, ended.ended(oidcSession("kokumi-2", "", now)))
		assert.Equal(t, map[string]string{endedSessionKey("kokumi-1"): now.UTC().Format(time.RFC3339)}, storedEntries(t, c).Data)
		assert.NotContains(t, endedSessionKey("kokumi-1"), "kokumi-1", "the record holds no identifiers")
	})

	t.Run("a provider session ends the Kokumi sessions signed in with it", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedProviderSessionKey(issuer, "op-1"), now))
		assert.True(t, ended.ended(oidcSession("kokumi-1", "op-1", now.Add(-time.Hour))))
		assert.True(t, ended.ended(oidcSession("kokumi-2", "op-1", now.Add(-time.Minute))))
		assert.False(t, ended.ended(oidcSession("kokumi-3", "op-2", now.Add(-time.Hour))))
	})

	t.Run("a provider user ends the sessions signed in before the logout", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedProviderUserKey(issuer, subject), now))
		assert.True(t, ended.ended(oidcSession("kokumi-1", "op-1", now.Add(-time.Hour))))
		assert.False(t, ended.ended(oidcSession("kokumi-2", "op-2", now.Add(time.Second))), "a sign-in after the logout")
	})

	t.Run("provider entries do not match admin sessions", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedProviderUserKey("", testUsername), now))
		admin := &verifiedToken{
			identity: &Identity{Subject: testUsername, Provider: providerAdmin},
			session:  sessionState{ID: "kokumi-1", AuthTime: now.Add(-time.Hour)},
		}
		assert.False(t, ended.ended(admin))
	})

	t.Run("the watch delivers what other replicas record, and wakes the waiters", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		changed := ended.changes()
		cm := resources.EndedSessionsConfigMap(testNS)
		cm.Data = map[string]string{endedSessionKey("kokumi-1"): now.Format(time.RFC3339), "garbage": "not a time"}
		ended.update(cm)
		assert.True(t, ended.ended(oidcSession("kokumi-1", "", now)))
		select {
		case <-changed:
		default:
			t.Fatal("an update must close the channel of changes")
		}
	})

	t.Run("an entry this server recorded survives an older watch event", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedSessionKey("kokumi-1"), now))
		ended.update(resources.EndedSessionsConfigMap(testNS))
		assert.True(t, ended.ended(oidcSession("kokumi-1", "", now)))
	})

	t.Run("pruning drops the entries no session can match any more", func(t *testing.T) {
		ended, c := newTestEndedSessions(t)
		require.NoError(t, ended.record(ctx, endedSessionKey("old"), now.Add(-25*time.Hour)))
		require.NoError(t, ended.record(ctx, endedSessionKey("new"), now.Add(-time.Hour)))
		require.NoError(t, ended.prune(ctx, now, defaultSessionTTL))
		assert.Equal(t, []string{endedSessionKey("new")}, slices.Collect(maps.Keys(storedEntries(t, c).Data)))
		require.NoError(t, ended.prune(ctx, now, defaultSessionTTL), "pruning again changes nothing")
	})

	t.Run("a record that cannot be written reports it", func(t *testing.T) {
		ended := newEndedSessions(fake.NewClientBuilder().Build(), testNS)
		assert.Error(t, ended.record(ctx, endedSessionKey("kokumi-1"), now))
		assert.False(t, ended.ended(oidcSession("kokumi-1", "", now)))
	})

	t.Run("no record ends nothing", func(t *testing.T) {
		var ended *endedSessions
		assert.False(t, ended.ended(oidcSession("kokumi-1", "", now)))
		assert.Nil(t, ended.changes())
		assert.Error(t, ended.record(ctx, endedSessionKey("kokumi-1"), now))
		assert.NoError(t, ended.prune(ctx, now, defaultSessionTTL))
	})
}

func TestSignOutEndsCopiesOfTheSession(t *testing.T) {
	ended, _ := newTestEndedSessions(t)
	mgr := &authManager{auth: newTestAuthenticator(t), adminLogin: true, logger: logr.Discard(), ended: ended}

	signedIn, err := mgr.signInAdmin(testUsername, testPassword)
	require.NoError(t, err)
	copied := heldBy(signedIn.cookies...)
	access := signedIn.tokens.accessToken

	assert.Equal(t, http.StatusOK, protectedRequest(mgr, access), "the session works before the sign-out")
	assertCleared(t, mgr.endSession(context.Background(), copied, testPostLogoutRedirect))

	assert.Equal(t, http.StatusUnauthorized, protectedRequest(mgr, access), "a copied access token stops working")
	_, err = mgr.refreshSession(context.Background(), copied, true)
	assert.ErrorIs(t, err, errSessionEnded, "a copied refresh cookie stops working")
}

func TestEventsStreamEndsWithItsSession(t *testing.T) {
	// stream opens a live-update stream of an admin session of mgr, and
	// returns a channel that is closed when the stream ends, and the ID of
	// the session.
	stream := func(t *testing.T, mgr *authManager) (<-chan struct{}, string) {
		t.Helper()
		res, err := mgr.signInAdmin(testUsername, testPassword)
		require.NoError(t, err)
		token, err := mgr.auth.parseToken(res.tokens.accessToken, accessTokenType)
		require.NoError(t, err)
		r := httptest.NewRequestWithContext(withVerifiedToken(t.Context(), token), http.MethodGet, "/api/v1/events", nil)
		done := make(chan struct{})
		go func() {
			handleEventsStream(newHub(), nil, mgr, time.Hour).ServeHTTP(httptest.NewRecorder(), r)
			close(done)
		}()
		return done, token.session.ID
	}
	stays := func(t *testing.T, done <-chan struct{}, msg string) {
		t.Helper()
		select {
		case <-done:
			t.Fatal(msg)
		case <-time.After(50 * time.Millisecond):
		}
	}
	ends := func(t *testing.T, done <-chan struct{}, msg string) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal(msg)
		}
	}

	t.Run("at a sign-out", func(t *testing.T) {
		ended, _ := newTestEndedSessions(t)
		mgr := &authManager{auth: newTestAuthenticator(t), adminLogin: true, logger: logr.Discard(), ended: ended}
		done, sessionID := stream(t, mgr)

		require.NoError(t, ended.record(context.Background(), endedSessionKey("other"), time.Now()))
		stays(t, done, "the stream must stay open when another session ends")

		require.NoError(t, ended.record(context.Background(), endedSessionKey(sessionID), time.Now()))
		ends(t, done, "the stream must end when its session ends")
	})

	t.Run("when the auth configuration ends its session", func(t *testing.T) {
		c := fake.NewClientBuilder().WithObjects(testTokenSecret(), newAuthSecret(map[string][]byte{
			secretKeyPasswordHash: mustBcrypt(t, testPassword),
		})).Build()
		mgr := &authManager{reader: c, apiReader: c, ns: testNS, logger: logr.Discard()}
		admin := &deliveryv1alpha1.AdminUserConfig{
			Enabled: true, Username: testUsername, SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}
		mgr.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: admin}))
		done, _ := stream(t, mgr)

		mgr.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: admin}))
		stays(t, done, "the stream must stay open while the configuration still signs it in")

		off := *admin
		off.Enabled = false
		mgr.reload(context.Background(), authKitchen(&deliveryv1alpha1.KitchenAuth{AdminUser: &off}))
		ends(t, done, "the stream must end when admin login is turned off")
	})
}

// accessTokenOf returns the access token of a sign-in or refresh response.
func accessTokenOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp loginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Token
}

func protectedRequest(mgr *authManager, access string) int {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodGet, testOrdersPath, nil)
	r.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	mgr.middleware(next).ServeHTTP(w, r)
	return w.Code
}
