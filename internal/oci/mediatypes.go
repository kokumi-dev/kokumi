package oci

// HelmChartLayerMediaType is the CNCF Helm OCI media type for chart content.
const HelmChartLayerMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// FluxContentMediaType is the Flux OCI media type for a kustomize/manifest
// content layer. Unlike ORAS "file" artifacts, Flux layers carry no
// io.deis.oras.content.unpack annotation, so they must be extracted explicitly.
const FluxContentMediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"

// ImageLayerMediaType is the media type of the single layer of an artifact
// pushed by Push.
const ImageLayerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"

// PreparationArtifactType is the artifactType of the rendered manifests of a
// Preparation.
const PreparationArtifactType = "application/vnd.kokumi.preparation.v1"

// PreparationConfigMediaType is the media type of the config blob that
// records the intent of a Preparation artifact.
const PreparationConfigMediaType = "application/vnd.kokumi.preparation.config.v1+json"

// SigstoreBundleMediaType is the artifactType of a referrer that carries a
// Sigstore bundle, and the media type of its single layer, as cosign v3
// writes them. Every signature and attestation Kokumi writes is such a
// referrer; they are told apart by their predicate type.
const SigstoreBundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"

// Manifest annotations of a Sigstore bundle referrer.
const (
	// AnnotationBundleContent names what the bundle signs.
	AnnotationBundleContent = "dev.sigstore.bundle.content"
	// BundleContentDSSEEnvelope is the AnnotationBundleContent of a bundle
	// that signs an in-toto statement in a DSSE envelope.
	BundleContentDSSEEnvelope = "dsse-envelope"
	// AnnotationBundlePredicateType is the predicate type of the signed
	// statement, as cosign records it.
	AnnotationBundlePredicateType = "dev.sigstore.bundle.predicateType"
	// AnnotationInTotoPredicateType is the predicate type of the signed
	// statement, as Trivy and GitHub discover attestations by it.
	AnnotationInTotoPredicateType = "in-toto.io/predicate-type"
)
