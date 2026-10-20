// Package evidence verifies, in the registry, the signed evidence a
// Preparation's artifact must carry before it is deployed. The registry is
// the record: nothing recorded in the Preparation's status is trusted.
package evidence

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"k8s.io/utils/lru"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// verifiedCacheSize bounds how many verified artifacts a Verifier remembers.
const verifiedCacheSize = 1024

// Verifier verifies the evidence of Preparations and remembers what
// verified. Digests are immutable, so a result holds until the trust
// changes; it is specific to the Order the provenance must name and to
// whether approvals are required. The zero value is ready to use.
type Verifier struct {
	once     sync.Once
	verified *lru.Cache
}

// verifiedKey identifies evidence that verified.
type verifiedKey struct {
	digest           string
	trust            string
	order            string
	approvalRequired bool
}

// Verify is Verify, for evidence it has not verified before: only then does
// it connect to the registry with connect. Errors of connect are returned as
// they are.
func (v *Verifier) Verify(
	ctx context.Context, trust *signing.TrustSet, preparation *deliveryv1alpha1.Preparation, order string,
	connect func(context.Context) (oci.Client, error),
) error {
	v.once.Do(func() { v.verified = lru.New(verifiedCacheSize) })
	key := verifiedKey{
		digest:           preparation.Spec.Artifact.Digest,
		trust:            trust.ID(),
		order:            order,
		approvalRequired: preparation.ApprovalPolicy() != nil,
	}
	if _, ok := v.verified.Get(key); ok {
		return nil
	}

	client, err := connect(ctx)
	if err != nil {
		return err
	}
	if err := Verify(ctx, client, trust, preparation, order); err != nil {
		return err
	}
	v.verified.Add(key, struct{}{})
	return nil
}

// Rejected reports evidence that is missing or does not verify, as opposed
// to a registry that could not be read. Reason is the condition reason that
// describes it.
type Rejected struct {
	Reason string
	Err    error
}

func (e *Rejected) Error() string { return e.Err.Error() }
func (e *Rejected) Unwrap() error { return e.Err }

// Verify verifies the evidence of preparation's artifact against trust: a
// signature, a provenance statement attesting a render for order
// ("namespace/name"), and, when the Preparation has an approval policy,
// sealed approvals with result Approved. Missing or unverified evidence
// fails with *Rejected.
func Verify(ctx context.Context, client oci.Client, trust *signing.TrustSet, preparation *deliveryv1alpha1.Preparation, order string) error {
	subject, err := oci.Parse(preparation.Spec.Artifact.OCIRef)
	if err != nil {
		return invalid(fmt.Errorf("invalid artifact reference: %w", err))
	}
	if subject.Digest != preparation.Spec.Artifact.Digest {
		return invalid(fmt.Errorf("artifact reference %s does not name digest %s", preparation.Spec.Artifact.OCIRef, preparation.Spec.Artifact.Digest))
	}

	if _, err := find(ctx, client, trust, subject, attestation.SignaturePredicateType); err != nil {
		return err
	}

	found, err := find(ctx, client, trust, subject, provenance.PredicateType)
	if err != nil {
		return err
	}
	var predicate provenance.Predicate
	if err := found.Predicate(&predicate); err != nil {
		return invalid(fmt.Errorf("provenance %s: %w", found.Digest, err))
	}
	if attested := predicate.BuildDefinition.ExternalParameters.Order; attested != order {
		return invalid(fmt.Errorf("provenance %s attests a render for Order %q, not %q", found.Digest, attested, order))
	}

	if preparation.ApprovalPolicy() == nil {
		return nil
	}

	found, err = find(ctx, client, trust, subject, approval.AttestationPredicateType)
	if err != nil {
		return err
	}
	var approvals approval.Predicate
	if err := found.Predicate(&approvals); err != nil {
		return invalid(fmt.Errorf("approvals %s: %w", found.Digest, err))
	}
	if approvals.Result != deliveryv1alpha1.ReasonApproved {
		return invalid(fmt.Errorf("approvals %s record result %q, not %q", found.Digest, approvals.Result, deliveryv1alpha1.ReasonApproved))
	}

	return nil
}

// find finds the verified attestation of predicateType about subject.
func find(ctx context.Context, client oci.Client, trust *signing.TrustSet, subject oci.Reference, predicateType string) (*attestation.Evidence, error) {
	found, err := attestation.Find(ctx, client, trust, subject, predicateType)
	switch {
	case errors.Is(err, attestation.ErrNotFound):
		return nil, &Rejected{Reason: deliveryv1alpha1.ReasonSignatureMissing, Err: err}
	case errors.Is(err, signing.ErrNotVerified):
		return nil, invalid(err)
	case err != nil:
		return nil, err
	}
	return found, nil
}

func invalid(err error) *Rejected {
	return &Rejected{Reason: deliveryv1alpha1.ReasonSignatureInvalid, Err: err}
}
