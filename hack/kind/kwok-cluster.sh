#!/usr/bin/env bash
# Creates a KWOK cluster for the local Kokumi to deploy to, and stores its
# credential with register-cluster.sh, in the Secret <cluster>-kubeconfig of
# the workspace namespace the Cluster lives in, platform by default.
#
# KWOK runs a real API server, etcd, controller manager and scheduler, with
# fake nodes and no kubelet: Argo CD deploys to it for real, and every Pod
# reports Running without a container or an image pull. Each takes seconds
# and about 400 MiB, half of a Kind spoke, so that a fleet fits on one machine.
#
# Its API server joins Docker's kind network, so Kokumi and Argo CD reach it at
# https://kwok-<kwok cluster>-kube-apiserver:6443, a name its certificate
# holds. The current kubeconfig context is left alone.
set -euo pipefail

name="${1:?usage: $0 <kwok cluster> [cluster] [namespace]}"
cluster="${2:-${name}}"
namespace="${3:-platform}"
kwokctl="${KWOKCTL:-kwokctl}"
docker="${DOCKER:-docker}"

# nodes is how many fake nodes the cluster has, enough for Pods that spread
# across nodes. They cost nothing but their objects.
nodes=3

dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT
admin="${dir}/admin.kubeconfig"

if ! "${kwokctl}" get clusters | grep -qx "${name}"; then
	echo "Creating KWOK cluster '${name}'..."
	"${kwokctl}" create cluster --name "${name}" --runtime docker --kubeconfig "${admin}" --wait 3m
fi
"${kwokctl}" get kubeconfig --name "${name}" >"${admin}"

apiserver="kwok-${name}-kube-apiserver"
if [ -z "$("${docker}" inspect --format '{{with index .NetworkSettings.Networks "kind"}}joined{{end}}' "${apiserver}")" ]; then
	echo "Connecting the API server of '${name}' to Docker's kind network..."
	"${docker}" network connect kind "${apiserver}"
fi

echo "Giving '${name}' ${nodes} fake nodes..."
"${kwokctl}" scale node --name "${name}" --replicas "${nodes}"

"$(dirname "$0")/register-cluster.sh" "${admin}" "https://${apiserver}:6443" "${cluster}" "${namespace}"
echo "Look at the cluster with: ${kwokctl} --name ${name} kubectl get pods -A"
