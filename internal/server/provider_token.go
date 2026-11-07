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

// deriveProviderTokenKey derives the key that seals providerTokenCookie from
// the token signing key, so replacing the signing key also invalidates the
// cookie.
func deriveProviderTokenKey(signingKey []byte) []byte {
	// HKDF fails only for a key longer than 255 hash lengths.
	key, _ := hkdf.Key(sha256.New, signingKey, nil, providerTokenKeyInfo, 32)
	return key
}

// sealProviderToken encrypts t as a compact JWE (dir, A256GCM) for
// providerTokenCookie. A sealed token too large for the cookie is an error.
func (a *authenticator) sealProviderToken(t providerToken) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: a.providerTokenKey}, nil)
	if err != nil {
		return "", err
	}
	encrypted, err := encrypter.Encrypt(payload)
	if err != nil {
		return "", err
	}
	sealed, err := encrypted.CompactSerialize()
	if err != nil {
		return "", err
	}
	if len(sealed) > maxProviderTokenCookieLen {
		return "", fmt.Errorf("the sealed refresh token has %d bytes, more than a cookie holds", len(sealed))
	}
	return sealed, nil
}

// openProviderToken decrypts a value of providerTokenCookie.
func (a *authenticator) openProviderToken(sealed string) (providerToken, error) {
	var t providerToken
	jwe, err := jose.ParseEncryptedCompact(sealed, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return t, err
	}
	payload, err := jwe.Decrypt(a.providerTokenKey)
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

// providerTokenOf returns the provider's refresh token of session s from
// sealed, the value of its provider token cookie, refusing a token of another
// session.
func (a *authenticator) providerTokenOf(sealed string, s sessionState) (string, error) {
	if sealed == "" {
		return "", errors.New("no provider token cookie")
	}
	t, err := a.openProviderToken(sealed)
	if err != nil {
		return "", fmt.Errorf("opening the provider token cookie: %w", err)
	}
	if t.SessionID != s.ID {
		return "", errors.New("the provider token belongs to another session")
	}
	return t.RefreshToken, nil
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
// section 6) in one request, so the provider decides whether the user stays
// signed in. A returned ID token must name the session's user (OpenID Connect
// Core section 12.2), whose username, email and groups it updates. It returns
// errProviderEndedSession when the provider refuses the grant (see
// refusesGrant), and another error when the provider cannot vouch for the
// user right now.
func (p *oidcProvider) checkSession(ctx context.Context, refreshToken string, id *Identity) (*providerCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, providerRequestTimeout)
	defer cancel()
	d, err := p.issuer.discover(ctx)
	if err != nil {
		return nil, err
	}
	// A refresh grant sends no redirect URI (RFC 6749 section 6).
	oauth := p.oauthConfig(d, "")
	token, err := oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if refusesGrant(err) {
		return nil, fmt.Errorf("%w: %w", errProviderEndedSession, err)
	}
	if err != nil {
		return nil, fmt.Errorf("refreshing at the OIDC provider: %w", err)
	}

	checked := &providerCheck{identity: id, refreshToken: token.RefreshToken}
	var idToken *oidc.IDToken
	if rawID, ok := token.Extra("id_token").(string); ok && rawID != "" {
		idToken, err = d.provider.Verifier(&oidc.Config{ClientID: oauth.ClientID}).Verify(ctx, rawID)
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

// refusesGrant reports whether a token endpoint's answer err refuses the
// refresh grant, so the user is no longer signed in at the provider. The
// endpoint names the reason with an OAuth error code (RFC 6749 section 5.2):
// invalid_grant for a revoked or expired grant or a deactivated user, and
// invalid_request, which Dex sends for a refresh token it no longer knows.
// The other codes, such as invalid_client for a wrong client secret, are the
// operator's to fix, not the user's; they, an answer without a code, such as
// a 5xx or a 429, and no answer at all do not end the session.
func refusesGrant(err error) bool {
	var answer *oauth2.RetrieveError
	if !errors.As(err, &answer) {
		return false
	}
	return answer.ErrorCode == "invalid_grant" || answer.ErrorCode == "invalid_request"
}

// revoke revokes a provider refresh token at the provider's
// revocation_endpoint (RFC 7009), if it has one. The client authenticates as
// at the token endpoint. Failures are returned for logging; the sign-out goes
// on.
func (p *oidcProvider) revoke(ctx context.Context, refreshToken string) error {
	ctx, cancel := context.WithTimeout(ctx, providerRequestTimeout)
	defer cancel()
	d, err := p.issuer.discover(ctx)
	if err != nil {
		return err
	}
	if d.revocationEndpoint == "" {
		return nil
	}
	form := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	inHeader := d.endpoint.AuthStyle == oauth2.AuthStyleInHeader
	if !inHeader {
		form.Set("client_id", p.oauth.ClientID)
		form.Set("client_secret", p.oauth.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.revocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if inHeader {
		req.SetBasicAuth(url.QueryEscape(p.oauth.ClientID), url.QueryEscape(p.oauth.ClientSecret))
	}
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
