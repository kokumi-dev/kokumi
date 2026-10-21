# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/kokumi-dev/kokumi:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Website

.PHONY: website-dev
website-dev: ## Start the local website development server (http://localhost:1313)
	hugo server --source docs/ --buildDrafts --navigateToChanged

.PHONY: website-build
website-build: ## Build the website into docs/public/
	hugo --source docs/ --minify

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# The e2e tests run on a Kind cluster of their own, which they build and load
# the manager image into; see test/e2e/README.md.
KIND_CLUSTER ?= kokumi-test-e2e
# E2E_LABELS selects specs by Ginkgo label filter, e.g. E2E_LABELS=promotion
# or E2E_LABELS='!slow'. Empty runs every spec.
E2E_LABELS ?=
# E2E_ARGOCD=real installs Argo CD and lets it deploy, instead of simulating
# its reports.
E2E_ARGOCD ?=
# ARTIFACTS receives the JUnit report, the diagnostics of failed specs and,
# when a spec failed, the logs of the Kind nodes and all their containers.
ARTIFACTS ?= $(shell pwd)/_artifacts
# The e2e cluster's kubeconfig is a file of its own, so that the e2e tests
# never deploy to the cluster of the current kubeconfig context.
E2E_KUBECONFIG ?= $(LOCALBIN)/$(KIND_CLUSTER).kubeconfig

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			mkdir -p "$(LOCALBIN)"; \
			$(KIND) create cluster --name $(KIND_CLUSTER) --config hack/kind/kokumi-test-e2e.yaml \
				--kubeconfig "$(E2E_KUBECONFIG)" ;; \
	esac
	@$(KIND) export kubeconfig --name $(KIND_CLUSTER) --kubeconfig "$(E2E_KUBECONFIG)"
	@KIND=$(KIND) hack/kind/registry-mirror.sh $(KIND_CLUSTER)

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet cosign ## Run the e2e tests on a Kind cluster of their own. Filter with E2E_LABELS.
	@mkdir -p "$(ARTIFACTS)"
	KUBECONFIG="$(E2E_KUBECONFIG)" KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) COSIGN="$(COSIGN)" \
		E2E_ARGOCD=$(E2E_ARGOCD) ARTIFACTS="$(ARTIFACTS)" \
		go test -tags=e2e ./test/e2e/ -v -timeout 2h -ginkgo.v \
		-ginkgo.label-filter="$(E2E_LABELS)" -ginkgo.junit-report="$(ARTIFACTS)/junit-e2e.xml" \
		|| { $(KIND) export logs "$(ARTIFACTS)/kind" --name $(KIND_CLUSTER); exit 1; }
	$(MAKE) cleanup-test-e2e

.PHONY: test-e2e-plan
test-e2e-plan: ## List the e2e specs and their labels without running them. Filter with E2E_LABELS.
	go test -tags=e2e ./test/e2e/ -v -count=1 -ginkgo.dry-run -ginkgo.v -ginkgo.label-filter="$(E2E_LABELS)"

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER) --kubeconfig "$(E2E_KUBECONFIG)"
	@rm -f "$(E2E_KUBECONFIG)"

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

KIND_CLUSTER_DEVELOPMENT ?= kokumi

.PHONY: kind-setup
kind-setup: ## Set up a Kind cluster for local development if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_DEVELOPMENT)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_DEVELOPMENT)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_DEVELOPMENT)'..."; \
			$(KIND) create cluster --config hack/kind/kokumi-cluster.yaml ;; \
	esac
	@KIND=$(KIND) hack/kind/registry-mirror.sh $(KIND_CLUSTER_DEVELOPMENT)

.PHONY: kind-delete
kind-delete: ## Delete the Kind cluster for local development
	$(KIND) delete cluster --name $(KIND_CLUSTER_DEVELOPMENT)

.PHONY: kind-load-image
kind-load-image: ## Load image into Kind cluster if it exists
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@if $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_DEVELOPMENT)$$"; then \
		echo "Loading image $(IMG) into Kind cluster '$(KIND_CLUSTER_DEVELOPMENT)'..."; \
		$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_DEVELOPMENT); \
	else \
		echo "Kind cluster '$(KIND_CLUSTER_DEVELOPMENT)' does not exist. Skipping image load."; \
	fi

.PHONY: push-artifacts
push-artifacts: cosign ## Pushes artifacts to local local registry
	COSIGN="$(COSIGN)" bash ./hack/push-artifacts.sh

.PHONY: oidc
oidc: ## Install local Dex OIDC (sidecar in kokumi-server) and wire Kitchen auth. See hack/oidc/README.md
	"$(KUBECTL)" apply -f hack/oidc/dex-config.yaml
	"$(KUBECTL)" patch deployment kokumi-server -n kokumi --type=strategic --patch-file hack/oidc/patch-server-deployment.yaml
	"$(KUBECTL)" -n kokumi rollout status deployment kokumi-server --timeout=180s
	"$(KUBECTL)" apply -f hack/oidc/dex-service.yaml
	"$(KUBECTL)" -n kokumi create secret generic kokumi-server-oidc \
		--from-literal=client-secret=kokumi-local-client-secret \
		--dry-run=client -o yaml | "$(KUBECTL)" apply -f -
	"$(KUBECTL)" -n kokumi patch kitchen default --type=merge \
		-p '{"spec":{"auth":{"adminUser":{"enabled":true},"oidc":{"issuerURL":"http://localhost:5556","clientID":"kokumi","scopes":["openid","profile","email","groups"]}}}}'

.PHONY: argocd
argocd: ## Installs Argo CD in the cluster
	"$(KUBECTL)" create namespace argocd --dry-run=client -o yaml | "$(KUBECTL)" apply -f -
	"$(KUBECTL)" apply -n argocd --server-side --force-conflicts -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/manifests/install.yaml
	"$(KUBECTL)" patch svc argocd-server -n argocd -p '{"spec": {"type": "NodePort", "ports": [{"port": 80,"targetPort": 8080,"nodePort": 30052}]}}'
	"$(KUBECTL)" -n argocd create secret generic manifests-registry-creds \
		--from-literal=type=oci \
		--from-literal=url=oci://kokumi-registry.kokumi.svc.cluster.local:5000 \
		--from-literal=insecureOCIForceHttp=true \
		--dry-run=client -o yaml | \
	"$(KUBECTL)" label -f - argocd.argoproj.io/secret-type=repo-creds --local -o yaml | \
	"$(KUBECTL)" apply -f -

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: build-server
build-server: ui-build fmt vet ## Build API server binary with embedded UI.
	go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: run-server
run-server: ui-build ## Run the API server locally (serves UI on :8080).
	go run ./cmd/server/main.go

.PHONY: ui-build
ui-build: ## Build the React UI into the embed path.
	cd ui && npm ci && npm run build
	rm -rf internal/server/web/dist
	cp -r ui/dist internal/server/web/dist

.PHONY: ui-dev
ui-dev: ## Start Vite dev server with proxy to local API server (requires run-server in parallel).
	cd ui && npm run dev

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	- $(CONTAINER_TOOL) buildx create --name kokumi-builder
	$(CONTAINER_TOOL) buildx use kokumi-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile .
	- $(CONTAINER_TOOL) buildx rm kokumi-builder

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/server && "$(KUSTOMIZE)" edit set image server=${IMG}
	cd config/scanner && "$(KUSTOMIZE)" edit set image scanner=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml
	echo "---" >> dist/install.yaml
	"$(KUSTOMIZE)" build config/signing >> dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/server && "$(KUSTOMIZE)" edit set image server=${IMG}
	cd config/scanner && "$(KUSTOMIZE)" edit set image scanner=${IMG}
	"$(KUSTOMIZE)" build config/signing | "$(KUBECTL)" apply -f -
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -
	"$(KUSTOMIZE)" build config/signing | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
COSIGN ?= $(LOCALBIN)/cosign

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.14.0
COSIGN_VERSION ?= v3.1.3
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}
	@chmod -R u+w "$(LOCALBIN)/k8s"

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: cosign
cosign: $(COSIGN) ## Download cosign locally if necessary.
$(COSIGN): $(LOCALBIN)
	$(call go-install-tool,$(COSIGN),github.com/sigstore/cosign/v3/cmd/cosign,$(COSIGN_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
