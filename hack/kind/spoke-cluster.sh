#!/usr/bin/env bash
# Creates a second Kind cluster, a spoke, for the local Kokumi to deploy to,
# and stores its credential with register-cluster.sh, in the Secret
# <cluster>-kubeconfig of the workspace namespace the Cluster lives in,
# platform by default.
#
# Both clusters share Docker's kind network, so Kokumi and Argo CD reach the
# spoke's API server at https://<spoke>-control-plane:6443. The current
# kubeconfig context is left alone.
set -euo pipefail

spoke="${1:?usage: $0 <spoke kind cluster> [cluster] [namespace]}"
cluster="${2:-spoke}"
namespace="${3:-platform}"
kind="${KIND:-kind}"

dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT
admin="${dir}/admin.kubeconfig"

if ! "${kind}" get clusters | grep -qx "${spoke}"; then
	echo "Creating Kind cluster '${spoke}'..."
	"${kind}" create cluster --name "${spoke}" --kubeconfig "${admin}" --wait 3m
fi
"${kind}" export kubeconfig --name "${spoke}" --kubeconfig "${admin}" >/dev/null

"$(dirname "$0")/register-cluster.sh" "${admin}" "https://${spoke}-control-plane:6443" "${cluster}" "${namespace}"
echo "Look at the spoke with: kind export kubeconfig --name ${spoke} --kubeconfig bin/${spoke}.kubeconfig"
