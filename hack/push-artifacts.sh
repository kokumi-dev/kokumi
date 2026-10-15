#!/usr/bin/env bash
set -euo pipefail

PKG_VERSION="${PKG_VERSION:-0.1.0}"
REPO="${REPO:-localhost:5000}"
COSIGN="${COSIGN:-cosign}"
# The testdata signed with hack/testdata/cosign.key, for the verified sample.
SIGNED_PACKAGE="${SIGNED_PACKAGE:-external-secrets}"

if ! GIT_ROOT=$(git rev-parse --show-toplevel 2>/dev/null); then
  echo "Not inside a git repository"
  exit 1
fi

GIT_REVISION="$(git rev-parse HEAD)"
IMAGE_SOURCE="https://github.com/kokumi-dev/kokumi"

helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/ >/dev/null 2>&1 || true
helm repo add external-secrets-operator https://charts.external-secrets.io/ >/dev/null 2>&1 || true
helm repo update >/dev/null

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Using temporary directory: $WORK_DIR"
echo "Pushing to: $REPO"
echo "Version: $PKG_VERSION"
echo "Source: $IMAGE_SOURCE"
echo "Revision: $GIT_REVISION"

# external-secrets: rendered into a single manifest.yaml (namespace + chart).
mkdir -p "$WORK_DIR/external-secrets"

echo "---" >> "$WORK_DIR/external-secrets/manifest.yaml"

kubectl create ns external-secrets --dry-run=client -o yaml \
  >> "$WORK_DIR/external-secrets/manifest.yaml"

helm template external-secrets external-secrets-operator/external-secrets \
  --version 2.0.1 \
  --namespace external-secrets \
  >> "$WORK_DIR/external-secrets/manifest.yaml"

# external-dns: rendered into multiple files at the artifact root
# (namespace.yaml + the chart's template files flattened out of templates/).
mkdir -p "$WORK_DIR/external-dns"

echo "---" >> "$WORK_DIR/external-dns/namespace.yaml"

kubectl create ns external-dns --dry-run=client -o yaml \
  >> "$WORK_DIR/external-dns/namespace.yaml"

helm template external-dns external-dns/external-dns \
  --version 1.20.0 \
  --namespace external-dns \
  --output-dir "$WORK_DIR/external-dns-render"

mv "$WORK_DIR"/external-dns-render/external-dns/templates/*.yaml "$WORK_DIR/external-dns/"
rm -rf "$WORK_DIR/external-dns-render"

for manifests_dir in "$WORK_DIR"/*; do
  package="$(basename "$manifests_dir")"

  echo "Pushing package: $package"

  (
    cd "$manifests_dir"
    oras push "${REPO}/kokumi-dev/testdata/${package}:${PKG_VERSION}" . \
      --annotation "org.opencontainers.image.source=${IMAGE_SOURCE}" \
      --annotation "org.opencontainers.image.revision=${GIT_REVISION}"
  )
done

# Sign one package with the testdata key, offline and without a transparency
# log, as a publisher signs a release. The verified sample Product trusts the
# key through spec.source.verify.
digest="$(oras resolve "${REPO}/kokumi-dev/testdata/${SIGNED_PACKAGE}:${PKG_VERSION}")"
echo "Signing package: ${SIGNED_PACKAGE}@${digest}"
case "$REPO" in
  localhost:*|127.0.0.1:*)
    # The local registry is anonymous; keep cosign away from credential
    # helpers that may prompt on the host.
    export DOCKER_CONFIG="$WORK_DIR/docker-config"
    mkdir -p "$DOCKER_CONFIG"
    echo '{}' > "$DOCKER_CONFIG/config.json"
    ;;
esac
COSIGN_PASSWORD="" "$COSIGN" sign --yes \
  --key "${GIT_ROOT}/hack/testdata/cosign.key" \
  --use-signing-config=false --tlog-upload=false \
  --allow-http-registry \
  "${REPO}/kokumi-dev/testdata/${SIGNED_PACKAGE}@${digest}"
