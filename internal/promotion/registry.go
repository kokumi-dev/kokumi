package promotion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/utils/lru"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// recordCacheSize bounds how many verified deployment records a
// RecordVerifier remembers.
const recordCacheSize = 1024

// ErrRecordInvalid reports a deployment record that did not verify: it is
// not signed by a trusted key, or it does not attest the deployment it is
// recorded for.
var ErrRecordInvalid = errors.New("deployment record did not verify")

// PushPromotion signs the promotion record of the pending entry of history,
// promoted by order (namespace/name) under mode after passing gates, and
// attaches it to the entry's artifact through client. The record is created
// at the entry's start, so a retry pushes equal bytes and finds the record it
// pushed before. It returns the digest of the record.
func PushPromotion(
	ctx context.Context, client oci.Client, signer *signing.Signer, order, mode string, history History, gates Gates,
) (string, error) {
	pending := history.Pending()
	if pending == nil {
		return "", errors.New("no promotion is pending")
	}
	statement, err := PromotionStatement(order, mode, *pending, history[1:].Deployed(), gates)
	if err != nil {
		return "", err
	}
	return push(ctx, client, signer, *pending, PromotionPredicateType, statement, pending.Started)
}

// PushDeployment signs the deployment record of e, a deployment by order
// (namespace/name) to targets that was healthy, and attaches it to e's
// artifact through client. The record is created at the time e was healthy.
// It returns the digest of the record.
func PushDeployment(
	ctx context.Context, client oci.Client, signer *signing.Signer, order string, e Entry, targets []Target,
) (string, error) {
	statement, err := DeploymentStatement(order, e, targets)
	if err != nil {
		return "", err
	}
	return push(ctx, client, signer, e, DeploymentPredicateType, statement, e.Healthy)
}

// push signs statement, a record of predicateType about the artifact of e,
// and attaches it to the artifact, created at created.
func push(
	ctx context.Context, client oci.Client, signer *signing.Signer, e Entry, predicateType string, statement []byte, created time.Time,
) (string, error) {
	subject, err := oci.Parse(e.OCIRef)
	if err != nil {
		return "", fmt.Errorf("parsing artifact reference: %w", err)
	}
	record, err := attestation.Push(ctx, client, signer, subject, predicateType, statement, created)
	if err != nil {
		return "", fmt.Errorf("failed to record the deployment of Preparation %q: %w", e.Preparation, err)
	}
	return record, nil
}

// RecordVerifier verifies the deployment records of upstream stages in the
// registry and remembers those that verified. Records are immutable, so a
// result holds until the trust changes. The zero value is ready to use.
type RecordVerifier struct {
	once     sync.Once
	verified *lru.Cache
}

// recordKey identifies a verified deployment record and the deployment it
// attests.
type recordKey struct {
	digest string
	trust  string
	order  string
	entry  string
}

// Verify verifies the deployment record of e, a deployment by order
// (namespace/name): it must be signed by a key of trust and attest e. It
// connects to the registry with connect only for a record it has not
// verified before. A record that does not verify is ErrRecordInvalid; errors
// of connect are returned as they are.
func (v *RecordVerifier) Verify(
	ctx context.Context, trust *signing.TrustSet, order string, e Entry, connect func(context.Context) (oci.Client, error),
) error {
	v.once.Do(func() { v.verified = lru.New(recordCacheSize) })
	key := recordKey{digest: e.DeploymentRecord, trust: trust.ID(), order: order,
		entry: e.Digest + "@" + timestamp(e.Healthy) + "#" + e.ProductRevision}
	if _, ok := v.verified.Get(key); ok {
		return nil
	}

	client, err := connect(ctx)
	if err != nil {
		return err
	}
	subject, err := oci.Parse(e.OCIRef)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRecordInvalid, err)
	}
	found, err := attestation.VerifyReferrer(ctx, client, trust, subject, e.DeploymentRecord, DeploymentPredicateType)
	if err != nil {
		if errors.Is(err, signing.ErrNotVerified) {
			return fmt.Errorf("%w: %w", ErrRecordInvalid, err)
		}
		return err
	}
	var attested DeploymentPredicate
	if err := found.Predicate(&attested); err != nil {
		return fmt.Errorf("%w: %w", ErrRecordInvalid, err)
	}
	if err := VerifyDeployment(attested, order, e); err != nil {
		return fmt.Errorf("%w: %w", ErrRecordInvalid, err)
	}

	v.verified.Add(key, struct{}{})
	return nil
}
