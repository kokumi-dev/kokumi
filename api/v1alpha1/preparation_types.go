/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// OrderSource defines the immutable base artifact for a preparation
type OrderSource struct {
	// oci is the OCI registry URL for the source manifests
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'oci'",message="must be a valid OCI URL"
	OCI string `json:"oci"`

	// baseDigest is the SHA256 digest of the base source artifact
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	BaseDigest string `json:"baseDigest"`
}

// RenderType identifies how the source was rendered to produce a Preparation.
type RenderType string

const (
	// RenderTypeManifest indicates the source was used as-is without a templating engine.
	RenderTypeManifest RenderType = "Manifest"
	// RenderTypeHelm indicates the source was rendered via Helm.
	RenderTypeHelm RenderType = "Helm"
)

// Renderer identifies what rendered the source into a Preparation.
type Renderer struct {
	// type records how the source was rendered to produce this Preparation.
	// One of "Manifest" or "Helm".
	// +required
	// +kubebuilder:validation:Enum=Manifest;Helm
	Type RenderType `json:"type"`

	// version is the version of the rendering engine: the Helm library
	// version for Helm, the Kokumi version for Manifest.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version"`

	// builder identifies the Kokumi controller that rendered the artifact,
	// normally its container image reference including digest. It is
	// reported by the controller itself.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Builder string `json:"builder"`
}

// Artifact defines the final immutable output of the rendering process
type Artifact struct {
	// ociRef is the full OCI reference including digest
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^oci://.*@sha256:[a-f0-9]{64}$`
	OCIRef string `json:"ociRef"`

	// digest is the SHA256 digest of the artifact
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
}

// GitSource records the SCM provenance of the base artifact that produced this
// Preparation. It is derived from the org.opencontainers.image.source,
// org.opencontainers.image.version, and org.opencontainers.image.revision
// annotations on the base artifact. Empty when the base artifact carries no git
// provenance.
type GitSource struct {
	// repo is the SCM repository URL of the base artifact's source
	// (org.opencontainers.image.source). Empty when the base artifact carries
	// no git provenance.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Repo string `json:"repo,omitempty"`

	// tag is the SCM tag of the base artifact's source files
	// (org.opencontainers.image.version). Empty when the base artifact carries
	// no git tag.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Tag string `json:"tag,omitempty"`

	// commitHash is the SCM commit SHA of the base artifact's source files
	// (org.opencontainers.image.revision). Empty when the base artifact carries
	// no git revision.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	CommitHash string `json:"commitHash,omitempty"`
}

// PreparationSpec defines the desired state of Preparation
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Preparation spec is immutable"
type PreparationSpec struct {
	// orderName is the name of the Order this preparation belongs to
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	OrderName string `json:"orderName"`

	// source defines the source artifact information
	// +kubebuilder:validation:Required
	Source OrderSource `json:"source"`

	// renderer identifies what rendered this preparation
	// +kubebuilder:validation:Required
	Renderer Renderer `json:"renderer"`

	// configHash is the SHA256 hash of the canonicalized order configuration
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]+$`
	ConfigHash string `json:"configHash"`

	// artifact defines the output artifact information
	// +kubebuilder:validation:Required
	Artifact Artifact `json:"artifact"`

	// commitMessage is the human-readable description of why this Preparation was created.
	// It is stored as the org.opencontainers.image.description annotation on the OCI artifact.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	CommitMessage string `json:"commitMessage,omitempty"`

	// parentDigest is the SHA256 digest of the artifact produced by the immediately preceding
	// Preparation for this Order. It is empty for the first Preparation and is stored as the
	// kokumi.dev/parent annotation on the OCI artifact, forming a git-like revision chain.
	// +optional
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ParentDigest string `json:"parentDigest,omitempty"`

	// gitSource records the SCM provenance of the base artifact that produced
	// this Preparation. Empty when the base artifact carries no git provenance.
	// +optional
	GitSource GitSource `json:"gitSource,omitempty"` //nolint:lll

	// approvalPolicy is the approval gate copied from the Order when this
	// Preparation was created. When set, the Preparation is only served once
	// the policy is satisfied. It is also recorded on the OCI artifact.
	// +optional
	ApprovalPolicy *ApprovalPolicy `json:"approvalPolicy,omitempty"`

	// images lists the container images the rendered manifests reference,
	// resolved to digests when the Preparation was rendered. The artifact's
	// config blob records the same list, with the reason for each image that
	// could not be resolved.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=512
	Images []PreparationImage `json:"images,omitempty"`
}

// PreparationImage is a container image the rendered manifests reference.
type PreparationImage struct {
	// ref is the image reference as written in the manifests before any
	// rewriting, for example "nginx:1.27".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Ref string `json:"ref"`

	// name is the normalized repository of ref, for example
	// "docker.io/library/nginx". Absent when ref is not a valid reference.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	Name string `json:"name,omitempty"`

	// digest is the manifest digest ref resolved to, with one of the
	// algorithms the OCI image spec registers. Absent when the image could
	// not be resolved, which only the Record policy allows.
	// +optional
	// +kubebuilder:validation:MaxLength=135
	// +kubebuilder:validation:Pattern=`^(sha256:[a-f0-9]{64}|sha512:[a-f0-9]{128})$`
	Digest string `json:"digest,omitempty"`

	// mediaType is the media type of the manifest digest names: an image
	// index for multi-platform images, an image manifest otherwise.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	MediaType string `json:"mediaType,omitempty"`

	// vendored is the reference the manifests pull the image from after it
	// was copied into its repository below the Order's destination, such as
	// <destination>/images/ghcr.io/example/app:1.0@sha256:.... Set under the
	// Vendor policy only.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Vendored string `json:"vendored,omitempty"`
}

// VoteResult describes how the latest vote of an approver is evaluated.
// +kubebuilder:validation:Enum=Counted;NotEligible
type VoteResult string

const (
	// VoteResultCounted means the vote contributes to the approval gate.
	VoteResultCounted VoteResult = "Counted"
	// VoteResultNotEligible means the approver is not in any allowed group.
	VoteResultNotEligible VoteResult = "NotEligible"
)

// ApprovalVote is the latest vote of a single approver on a Preparation.
type ApprovalVote struct {
	// approvalName is the name of the Approval carrying this vote.
	// +required
	// +kubebuilder:validation:MaxLength=253
	ApprovalName string `json:"approvalName"`

	// issuer is the OIDC issuer of the approver.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	Issuer string `json:"issuer"`

	// subject is the OIDC subject of the approver.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Subject string `json:"subject"`

	// username is the human-readable name of the approver.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Username string `json:"username,omitempty"`

	// decision is the verdict of the vote.
	// +required
	Decision ApprovalDecision `json:"decision"`

	// result reports whether the vote counts towards the gate.
	// +required
	Result VoteResult `json:"result"`

	// submittedTime is when the vote was submitted.
	// +required
	SubmittedTime metav1.MicroTime `json:"submittedTime"`
}

// ApprovalAttestation references the OCI referrer that records the sealed
// approvals of a Preparation: a Sigstore bundle signed by the Kitchen key.
type ApprovalAttestation struct {
	// ociRef is the full OCI reference of the attestation including digest.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	OCIRef string `json:"ociRef"`

	// digest is the manifest digest of the attestation.
	// +required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
}

// PreparationApprovalStatus aggregates the Approvals of a Preparation.
type PreparationApprovalStatus struct {
	// requiredApprovals is the number of approvals required by the policy.
	// +optional
	RequiredApprovals int32 `json:"requiredApprovals"`

	// approvedCount is the number of eligible approvers whose latest vote is Approve.
	// +optional
	ApprovedCount int32 `json:"approvedCount"`

	// rejectedCount is the number of eligible approvers whose latest vote is Reject.
	// +optional
	RejectedCount int32 `json:"rejectedCount"`

	// ineligibleCount is the number of approvers not in any allowed group.
	// +optional
	IneligibleCount int32 `json:"ineligibleCount"`

	// submissionCount is the total number of Approvals for this Preparation,
	// including superseded ones.
	// +optional
	SubmissionCount int32 `json:"submissionCount"`

	// votes lists the latest vote of each approver. Once sealed, only these
	// votes are considered.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	Votes []ApprovalVote `json:"votes,omitempty"`

	// sealedTime is when the votes were locked because the Preparation was
	// promoted. Votes submitted afterwards are ignored.
	// +optional
	SealedTime *metav1.Time `json:"sealedTime,omitempty"`

	// attestation references the OCI artifact recording the sealed votes.
	// +optional
	Attestation *ApprovalAttestation `json:"attestation,omitempty"`
}

// AttestationType identifies the kind of evidence an attestation carries.
// +kubebuilder:validation:Enum=Signature;Provenance
type AttestationType string

const (
	// AttestationTypeSignature is the signature of the artifact
	// (predicate https://sigstore.dev/cosign/sign/v1).
	AttestationTypeSignature AttestationType = "Signature"
	// AttestationTypeProvenance is the SLSA provenance of the artifact.
	AttestationTypeProvenance AttestationType = "Provenance"
)

// AttestationReference references an OCI referrer that carries evidence
// about the Preparation's artifact: a Sigstore bundle signed by the Kitchen
// key.
type AttestationReference struct {
	// type is the kind of evidence the attestation carries.
	// +required
	Type AttestationType `json:"type"`

	// ociRef is the full OCI reference of the attestation including digest.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	OCIRef string `json:"ociRef"`

	// digest is the manifest digest of the attestation.
	// +required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
}

// PreparationStatus defines the observed state of Preparation.
type PreparationStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Preparation resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// attestations lists the latest attestation of each type that signs the
	// artifact or records how it was produced. The approvals attestation is
	// not listed here; it is recorded in approval.attestation.
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Attestations []AttestationReference `json:"attestations,omitempty"`

	// approval aggregates the Approvals of this Preparation. Only set when
	// spec.approvalPolicy is set.
	// +optional
	Approval *PreparationApprovalStatus `json:"approval,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Order",type=string,JSONPath=`.spec.orderName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].reason`
// +kubebuilder:printcolumn:name="Approved",type=string,JSONPath=`.status.conditions[?(@.type=='Approved')].reason`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.spec.artifact.digest`,priority=1
// +kubebuilder:printcolumn:name="Signed",type=string,JSONPath=`.status.conditions[?(@.type=='Signed')].status`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:resource:shortName=prep
// +kubebuilder:selectablefield:JSONPath=`.spec.orderName`

// Preparation is the Schema for the preparations API
type Preparation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Preparation
	// +required
	Spec PreparationSpec `json:"spec"`

	// status defines the observed state of Preparation
	// +optional
	Status PreparationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PreparationList contains a list of Preparation
type PreparationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Preparation `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Preparation{}, &PreparationList{})
		return nil
	})
}
