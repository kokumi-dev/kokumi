package server

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const (
	// providerTokenCookie holds the OIDC provider's refresh token of a
	// session, sealed so that only the server can read it.
	providerTokenCookie = "kokumi.oidc.refresh"
	// providerTokenKeyInfo is the HKDF info that derives the key sealing
	// providerTokenCookie from the token signing key.
	providerTokenKeyInfo = "kokumi.oidc.refresh cookie"
	// maxProviderTokenCookieLen is the longest sealed token kept in a cookie;
	// browsers drop cookies larger than 4096 bytes.
	maxProviderTokenCookieLen = 4000
	// providerRequestTimeout bounds one request to the provider's token or
	// revocation endpoint.
	providerRequestTimeout = 10 * time.Second
)

// errProviderEndedSession is returned when the provider refuses to vouch for
// the user any longer: it refused the refresh grant, or its answer names
// another user.
var errProviderEndedSession = errors.New("the OIDC provider ended the session")

// providerToken is the content of providerTokenCookie.
type providerToken struct {
	// RefreshToken is the provider's refresh token.
	RefreshToken string `json:"rt"`
	// SessionID is the Kokumi session the refresh token belongs to.
	SessionID string `json:"sid"`
}

// providerTokenKey derives the key that seals providerTokenCookie from the
// token signing key, so replacing the signing key also invalidates the cookie.
func (a *authenticator) providerTokenKey() ([]byte, error) {
	return hkdf.Key(sha256.New, a.signingKey, nil, providerTokenKeyInfo, 32)
}

// sealProviderToken encrypts t as a compact JWE (dir, A256GCM).
func (a *authenticator) sealProviderToken(t providerToken) (string, error) {
	key, err := a.providerTokenKey()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: key}, nil)
	if err != nil {
		return "", err
	}
	sealed, err := encrypter.Encrypt(payload)
	if err != nil {
		return "", err
	}
	return sealed.CompactSerialize()
}

// openProviderToken decrypts a value of providerTokenCookie.
func (a *authenticator) openProviderToken(sealed string) (providerToken, error) {
	var t providerToken
	key, err := a.providerTokenKey()
	if err != nil {
		return t, err
	}
	jwe, err := jose.ParseEncryptedCompact(sealed, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return t, err
	}
	payload, err := jwe.Decrypt(key)
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(payload, &t); err != nil {
		return t, err
	}
	if t.RefreshToken == "" || t.SessionID == "" {
		return t, errors.New("sealed provider token is incomplete")
	}
	return t, nil
}

// providerTokenFromCookie returns the provider token of session s from its
// cookie, refusing a token of another session.
func (a *authenticator) providerTokenFromCookie(r *http.Request, s sessionState) (string, error) {
	c, err := r.Cookie(providerTokenCookie)
	if err != nil {
		return "", errors.New("no provider token cookie")
	}
	t, err := a.openProviderToken(c.Value)
	if err != nil {
		return "", fmt.Errorf("opening the provider token cookie: %w", err)
	}
	if t.SessionID != s.ID {
		return "", errors.New("the provider token belongs to another session")
	}
	return t.RefreshToken, nil
}

// setProviderTokenCookie writes the sealed provider token; it expires with
// the refresh cookie.
func setProviderTokenCookie(w http.ResponseWriter, r *http.Request, sealed string, expires time.Time) {
	setCookie(w, r, providerTokenCookie, sealed, refreshCookiePath, maxAgeUntil(expires))
}

// nextProviderCheck is when the provider's tokens of a token response
// expire: the access token's expiry, else the ID token's, else now, which
// checks again at the next refresh.
func nextProviderCheck(now time.Time, token *oauth2.Token, idToken *oidc.IDToken) time.Time {
	switch {
	case !token.Expiry.IsZero():
		return token.Expiry
	case idToken != nil:
		return idToken.Expiry
	default:
		return now
	}
}

// providerCheck is the outcome of a successful check at the provider.
type providerCheck struct {
	identity *Identity
	// next is when to ask the provider again.
	next time.Time
	// refreshToken is the refresh token for the next check, rotated or not.
	refreshToken string
}

// checkSession redeems the session's provider refresh token (RFC 6749
// section 6), so the provider decides whether the user stays signed in. A
// returned ID token must name the session's user (OpenID Connect Core
// section 12.2), whose username, email and groups it updates. It returns
// errProviderEndedSession when the provider refuses, and another error when
// it cannot be asked.
//
// The token endpoint reports OAuth errors, such as invalid_grant for a
// deactivated user or a revoked grant, with a 4xx status (RFC 6749 section
// 5.2), and trouble of its own with a 5xx. Every 4xx but 429 counts as a
// refusal, since providers differ in the error code they send for an unknown
// refresh token; a 5xx, a 429 or no answer means the provider cannot be asked.
func (p *oidcProvider) checkSession(ctx context.Context, r *http.Request, refreshToken string, id *Identity) (*providerCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, providerRequestTimeout)
	defer cancel()
	provider, err := p.issuer.discover(ctx)
	if err != nil {
		return nil, err
	}
	oauth := p.oauthConfig(r, provider)
	token, err := oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		var answer *oauth2.RetrieveError
		if errors.As(err, &answer) && answer.Response != nil && refusesGrant(answer.Response.StatusCode) {
			return nil, fmt.Errorf("%w: %w", errProviderEndedSession, err)
		}
		return nil, fmt.Errorf("refreshing at the OIDC provider: %w", err)
	}

	checked := &providerCheck{identity: id, refreshToken: token.RefreshToken}
	var idToken *oidc.IDToken
	if rawID, ok := token.Extra("id_token").(string); ok && rawID != "" {
		idToken, err = provider.Verifier(&oidc.Config{ClientID: oauth.ClientID}).Verify(ctx, rawID)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid ID token: %w", errProviderEndedSession, err)
		}
		var claims map[string]any
		if err := idToken.Claims(&claims); err != nil {
			return nil, fmt.Errorf("%w: reading the ID token: %w", errProviderEndedSession, err)
		}
		refreshed, err := identityFromClaims(claims, p.usernameClaim, p.emailClaim, p.groupsClaim)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errProviderEndedSession, err)
		}
		refreshed.Issuer = idToken.Issuer
		if refreshed.Issuer != id.Issuer || refreshed.Subject != id.Subject {
			return nil, fmt.Errorf("%w: the ID token names another user", errProviderEndedSession)
		}
		checked.identity = refreshed
	}
	checked.next = nextProviderCheck(time.Now(), token, idToken)
	return checked, nil
}

// refusesGrant reports whether a token endpoint's status refuses the grant,
// as opposed to the endpoint being unable to answer.
func refusesGrant(status int) bool {
	return status >= 400 && status < 500 && status != http.StatusTooManyRequests
}

// revoke revokes a provider refresh token at the provider's
// revocation_endpoint (RFC 7009), if it has one. Failures are returned for
// logging; the sign-out goes on.
func (p *oidcProvider) revoke(ctx context.Context, refreshToken string) error {
	ctx, cancel := context.WithTimeout(ctx, providerRequestTimeout)
	defer cancel()
	provider, err := p.issuer.discover(ctx)
	if err != nil {
		return err
	}
	var metadata struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return fmt.Errorf("reading oidc provider metadata: %w", err)
	}
	if metadata.RevocationEndpoint == "" {
		return nil
	}
	form := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(p.oauth.ClientID), url.QueryEscape(p.oauth.ClientSecret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoking the provider refresh token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoking the provider refresh token: %s", resp.Status)
	}
	return nil
}
