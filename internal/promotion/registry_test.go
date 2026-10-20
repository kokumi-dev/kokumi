package promotion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

func TestPushPromotion(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	client := oci.NewFakeClient(nil)

	_, err := PushPromotion(ctx, client, signer, stagingKey, "Manual", nil, Gates{})
	require.Error(t, err, "nothing is pending")

	h := History{}.Open(candidate(app1, revision1), t0)
	record, err := PushPromotion(ctx, client, signer, stagingKey, "Manual", h, Gates{})
	require.NoError(t, err)
	again, err := PushPromotion(ctx, client, signer, stagingKey, "Manual", h, Gates{})
	require.NoError(t, err)
	assert.Equal(t, record, again, "a retry pushes equal bytes")
}

func TestRecordVerifier(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	client := oci.NewFakeClient(nil)

	h := deployed(nil, candidate(app1, revision1), t0, record1)
	h, _ = h.Observe(Observation{Revision: h[0].Digest, Health: HealthHealthy}, t0.Add(time.Minute))
	e := h[0]
	record, err := PushDeployment(ctx, client, signer, stagingKey, e, []Target{{Server: "https://kubernetes.default.svc", Namespace: "shop"}})
	require.NoError(t, err)
	e.DeploymentRecord = record

	connects := 0
	connect := func(context.Context) (oci.Client, error) {
		connects++
		return client, nil
	}
	var verifier RecordVerifier
	require.NoError(t, verifier.Verify(ctx, signer.TrustSet(), stagingKey, e, connect))
	require.NoError(t, verifier.Verify(ctx, signer.TrustSet(), stagingKey, e, connect))
	assert.Equal(t, 1, connects, "a verified record is remembered")

	err = verifier.Verify(ctx, signingtest.NewSigner(t).TrustSet(), stagingKey, e, connect)
	require.ErrorIs(t, err, ErrRecordInvalid, "signed by a key that is not trusted")

	other := e
	other.Healthy = e.Healthy.Add(time.Hour)
	err = verifier.Verify(ctx, signer.TrustSet(), stagingKey, other, connect)
	require.ErrorIs(t, err, ErrRecordInvalid, "it attests another deployment")
	require.ErrorIs(t, err, ErrRecordMismatch)

	err = verifier.Verify(ctx, signer.TrustSet(), "shop/other", e, connect)
	require.ErrorIs(t, err, ErrRecordInvalid, "it attests a deployment by another Order")

	unreachable := errors.New("registry unreachable")
	err = verifier.Verify(ctx, signer.TrustSet(), "shop/third", e, func(context.Context) (oci.Client, error) { return nil, unreachable })
	require.ErrorIs(t, err, unreachable, "errors of connect are returned as they are")
	assert.NotErrorIs(t, err, ErrRecordInvalid)
}
