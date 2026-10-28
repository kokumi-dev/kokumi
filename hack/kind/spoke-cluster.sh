#!/usr/bin/env bash
# Creates a second Kind cluster, a spoke, for the local Kokumi to deploy to,
# and stores its credential the way the operator guide tells admins to: a
# ServiceAccount with a token on the spoke, and a kubeconfig of it in the
# Secret <cluster>-kubeconfig of the workspace namespace the Cluster lives in,
# platform by default, which it creates when it is missing. It prints the
# Cluster to apply that names the Secret.
#
# Both clusters share Docker's kind network, so Kokumi and Argo CD reach the
# spoke's API server at https://<spoke>-control-plane:6443. The current
# kubeconfig context is left alone.
set -euo pipefail

spoke="${1:?usage: $0 <spoke kind cluster> [cluster] [namespace]}"
cluster="${2:-spoke}"
namespace="${3:-platform}"
kind="${KIND:-kind}"
kubectl="${KUBECTL:-kubectl}"

dir="$(mktemp -d)"
trap 'rm -rf "${dir}"' EXIT
admin="${dir}/admin.kubeconfig"

if ! "${kind}" get clusters | grep -qx "${spoke}"; then
	echo "Creating Kind cluster '${spoke}'..."
	"${kind}" create cluster --name "${spoke}" --kubeconfig "${admin}" --wait 3m
fi
"${kind}" export kubeconfig --name "${spoke}" --kubeconfig "${admin}" >/dev/null
on_spoke() { "${kubectl}" --kubeconfig "${admin}" "$@"; }

echo "Creating the ServiceAccount kokumi on '${spoke}', with a token..."
on_spoke create serviceaccount kokumi -n kube-system --dry-run=client -o yaml | on_spoke apply -f -
on_spoke create clusterrolebinding kokumi --clusterrole=cluster-admin \
	--serviceaccount=kube-system:kokumi --dry-run=client -o yaml | on_spoke apply -f -
on_spoke apply -f - <<EOF
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
	token="$(on_spoke get secret kokumi-token -n kube-system -o jsonpath='{.data.token}')"
	[ -z "${token}" ] || break
	sleep 1
done
if [ -z "${token}" ]; then
	echo "The token controller of '${spoke}' wrote no token into kube-system/kokumi-token within 60s" >&2
	exit 1
fi
on_spoke get secret kokumi-token -n kube-system -o jsonpath='{.data.ca\.crt}' | base64 -d >"${dir}/ca.crt"

server="https://${spoke}-control-plane:6443"
kubeconfig="${dir}/spoke.kubeconfig"
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
echo "Look at the spoke with: kind export kubeconfig --name ${spoke} --kubeconfig bin/${spoke}.kubeconfig"
