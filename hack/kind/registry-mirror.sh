#!/usr/bin/env bash
# Lets the nodes of a Kind cluster pull images from the bundled registry under
# the name Kokumi vendors them to, kokumi-registry.kokumi.svc.cluster.local:5000.
# Nodes resolve names with node DNS, not cluster DNS, and expect TLS, so
# containerd is pointed at the registry's NodePort over plain HTTP through a
# hosts.toml, as in https://kind.sigs.k8s.io/docs/user/local-registry/.
set -euo pipefail

cluster="${1:?usage: $0 <kind cluster name>}"
kind="${KIND:-kind}"
registry="kokumi-registry.kokumi.svc.cluster.local:5000"
node_port="30050"
dir="/etc/containerd/certs.d/${registry}"

for node in $("${kind}" get nodes --name "${cluster}"); do
	docker exec "${node}" mkdir -p "${dir}"
	docker exec -i "${node}" cp /dev/stdin "${dir}/hosts.toml" <<EOF
[host."http://localhost:${node_port}"]
  capabilities = ["pull", "resolve"]
EOF
done
