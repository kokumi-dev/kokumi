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

// MenuArtifactType is the artifactType of the manifests pre-rendered by a Menu.
const MenuArtifactType = "application/vnd.kokumi.menu.v1"

// PreparationConfigMediaType is the media type of the config blob that
// records the intent of a Preparation or pre-rendered Menu artifact.
const PreparationConfigMediaType = "application/vnd.kokumi.preparation.config.v1+json"

// ProvenanceArtifactType is the artifactType of the referrer that carries the
// SLSA provenance of a rendered artifact.
const ProvenanceArtifactType = "application/vnd.kokumi.provenance.v1+json"

// InTotoStatementMediaType is the media type of an in-toto statement blob.
const InTotoStatementMediaType = "application/vnd.in-toto+json"

// ApprovalsArtifactType is the artifactType of the referrer that archives the
// approvals of a Preparation.
const ApprovalsArtifactType = "application/vnd.kokumi.approvals.v1+json"
