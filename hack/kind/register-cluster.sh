#!/usr/bin/env bash
# Stores the credential of a cluster for the local Kokumi the way the operator
# guide tells admins to: a ServiceAccount with a token on the cluster, and a
# kubeconfig of it in the Secret <cluster>-kubeconfig of the workspace
# namespace the Cluster lives in, which it creates when it is missing. It
# prints the Cluster to apply that names the Secret. spoke-cluster.sh and
# kwok-cluster.sh run it once they have created their cluster.
#
# admin is a kubeconfig that reaches the cluster from here; server is where
# Kokumi and Argo CD reach it from inside the Kind cluster they run in.
set -euo pipefail

usage="usage: $0 <admin kubeconfig> <server> <cluster> <namespace>"
admin="${1:?${usage}}"
server="${2:?${usage}}"
cluster="${3:?${usage}}"
namespace="${4:?${usage}}"
kubectl="${KUBECTL:-kubectl}"

dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT
on_cluster() { "${kubectl}" --kubeconfig "${admin}" "$@"; }

echo "Creating the ServiceAccount kokumi on '${cluster}', with a token..."
on_cluster create serviceaccount kokumi -n kube-system --dry-run=client -o yaml | on_cluster apply -f -
on_cluster create clusterrolebinding kokumi --clusterrole=cluster-admin \
	--serviceaccount=kube-system:kokumi --dry-run=client -o yaml | on_cluster apply -f -
on_cluster apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: kokumi-token
  namespace: kube-system
  annotations:
    kubernetes.io/service-account.name: kokumi
type: kubernetes.io/service-account-token
EOF
token=""
for _ in $(seq 60); do
	token="$(on_cluster get secret kokumi-token -n kube-system -o jsonpath='{.data.token}')"
	[ -z "${token}" ] || break
	sleep 1
done
if [ -z "${token}" ]; then
	echo "The token controller of '${cluster}' wrote no token into kube-system/kokumi-token within 60s" >&2
	exit 1
fi
on_cluster get secret kokumi-token -n kube-system -o jsonpath='{.data.ca\.crt}' | base64 -d >"${dir}/ca.crt"

kubeconfig="${dir}/cluster.kubeconfig"
"${kubectl}" config --kubeconfig "${kubeconfig}" set-cluster "${cluster}" --server "${server}" \
	--certificate-authority "${dir}/ca.crt" --embed-certs >/dev/null
"${kubectl}" config --kubeconfig "${kubeconfig}" set-credentials kokumi --token "$(echo "${token}" | base64 -d)" >/dev/null
"${kubectl}" config --kubeconfig "${kubeconfig}" set-context "${cluster}" --cluster "${cluster}" --user kokumi >/dev/null
"${kubectl}" config --kubeconfig "${kubeconfig}" use-context "${cluster}" >/dev/null

echo "Storing its kubeconfig in the Secret ${namespace}/${cluster}-kubeconfig..."
"${kubectl}" create namespace "${namespace}" --dry-run=client -o yaml | "${kubectl}" apply -f -
"${kubectl}" create secret generic "${cluster}-kubeconfig" -n "${namespace}" \
	--from-file=value="${kubeconfig}" --dry-run=client -o yaml | "${kubectl}" apply -f -

echo "Done. Create the Cluster '${cluster}' that names it, e.g.:"
cat <<EOF
kubectl apply -n ${namespace} -f - <<YAML
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Cluster
metadata:
  name: ${cluster}
spec:
  connection: Kubeconfig
  kubeconfig:
    secretRef:
      name: ${cluster}-kubeconfig
YAML
EOF
