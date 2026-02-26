#!/usr/bin/env bash
set -euo pipefail

PKG_VERSION="${PKG_VERSION:-0.1.0}"
REPO="${REPO:-localhost:5000}"

if ! GIT_ROOT=$(git rev-parse --show-toplevel 2>/dev/null); then
  echo "Not inside a git repository"
  exit 1
fi

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Using temporary directory: $WORK_DIR"
echo "Pushing to: $REPO"
echo "Version: $PKG_VERSION"

mkdir -p "$WORK_DIR/kube-prometheus-stack"

kubectl create ns kube-prometheus-stack --dry-run=client -o yaml \
  > "$WORK_DIR/kube-prometheus-stack/manifest.yaml"

echo "---" >> "$WORK_DIR/kube-prometheus-stack/manifest.yaml"

helm template kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --version 82.4.0 \
  --namespace kube-prometheus-stack \
  >> "$WORK_DIR/kube-prometheus-stack/manifest.yaml"

mkdir -p "$WORK_DIR/external-secrets"

kubectl create ns external-secrets --dry-run=client -o yaml \
  > "$WORK_DIR/external-secrets/manifest.yaml"

echo "---" >> "$WORK_DIR/external-secrets/manifest.yaml"

helm template external-secrets external-secrets-operator/external-secrets \
  --version 2.0.1 \
  --namespace external-secrets \
  >> "$WORK_DIR/external-secrets/manifest.yaml"

for manifests_dir in "$WORK_DIR"/*; do
  package="$(basename "$manifests_dir")"

  echo "Pushing package: $package"

  (
    cd "$manifests_dir"
    oras push "${REPO}/recipe/${package}:${PKG_VERSION}" .
  )
done
