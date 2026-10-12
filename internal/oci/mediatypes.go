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

// InTotoStatementMediaType is the media type of an in-toto statement blob.
const InTotoStatementMediaType = "application/vnd.in-toto+json"

// ApprovalsArtifactType is the artifactType of the referrer that archives the
// approvals of a Preparation.
const ApprovalsArtifactType = "application/vnd.kokumi.approvals.v1+json"
