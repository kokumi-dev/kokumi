package server

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

// clustersResource is the resource of Clusters.
const clustersResource = "clusters"

// kubeconfigSecretSuffix names the Secret the UI stores a cluster's
// kubeconfig in: <cluster>-kubeconfig, as Cluster API names it.
const kubeconfigSecretSuffix = "-kubeconfig"

// handleListClusters handles GET /api/v1/clusters, of one namespace with
// ?namespace=, each with the Environments of its namespace that select it.
// With ?labelSelector=, in Kubernetes' label selector syntax, it lists the
// Clusters an Environment with that clusterSelector selects: those whose
// labels match and that are not being deleted.
func handleListClusters(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var listOpts []client.ListOption
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}
		query, selecting := r.URL.Query()["labelSelector"]
		if selecting {
			selector, err := labels.Parse(query[0])
			if err != nil {
				respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid labelSelector: %s", err))
				return
			}
			listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: selector})
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		clusters := &deliveryv1alpha1.ClusterList{}
		if err := uc.list(r.Context(), clusters, listOpts...); err != nil {
			respondForbiddenOrError(w, err, "failed to list clusters")
			return
		}
		if selecting {
			clusters.Items = slices.DeleteFunc(clusters.Items, func(c deliveryv1alpha1.Cluster) bool { return !c.DeletionTimestamp.IsZero() })
		}
		environments := &deliveryv1alpha1.EnvironmentList{}
		var envOpts []client.ListOption
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			envOpts = append(envOpts, client.InNamespace(ns))
		}
		_ = uc.list(r.Context(), environments, envOpts...) // only names which Environments select a cluster

		respondJSON(w, http.StatusOK, clustersToDTO(clusters.Items, environments.Items, deps.installNamespace))
	}
}

// handleGetCluster handles GET /api/v1/clusters/{namespace}/{name}.
func handleGetCluster(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		c, ok := getCluster(w, r, uc)
		if !ok {
			return
		}
		environments := &deliveryv1alpha1.EnvironmentList{}
		_ = uc.list(r.Context(), environments, client.InNamespace(c.Namespace))
		respondJSON(w, http.StatusOK, clustersToDTO([]deliveryv1alpha1.Cluster{*c}, environments.Items, deps.installNamespace)[0])
	}
}

// handleClusterDeployments handles
// GET /api/v1/clusters/{namespace}/{name}/deployments: what each Order of
// the Environments that select the Cluster deploys there. A Serving lists
// only the clusters that do not run its Preparation healthily, so this is
// worked out per cluster, on request, rather than kept in a status.
func handleClusterDeployments(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		c, ok := getCluster(w, r, uc)
		if !ok {
			return
		}
		inNamespace := client.InNamespace(c.Namespace)
		environments := &deliveryv1alpha1.EnvironmentList{}
		orders := &deliveryv1alpha1.OrderList{}
		servings := &deliveryv1alpha1.ServingList{}
		for _, list := range []client.ObjectList{environments, orders, servings} {
			if err := uc.list(r.Context(), list, inNamespace); err != nil {
				respondForbiddenOrError(w, err, "failed to list what deploys to the cluster")
				return
			}
		}

		respondJSON(w, http.StatusOK, clusterDeployments(c, environments.Items, orders.Items, servings.Items))
	}
}

// clusterDeployments returns what each Order of environments that select c
// deploys there, by Order name.
func clusterDeployments(
	c *deliveryv1alpha1.Cluster, environments []deliveryv1alpha1.Environment, orders []deliveryv1alpha1.Order, servings []deliveryv1alpha1.Serving,
) []ClusterDeploymentDTO {
	selecting := map[string]bool{}
	for i := range environments {
		if s, err := resolve.ClusterSelectionOf(&environments[i]); err == nil && s.Selects(c) {
			selecting[environments[i].Name] = true
		}
	}
	servingOf := map[string]*deliveryv1alpha1.Serving{}
	for i := range servings {
		servingOf[servings[i].Spec.OrderName] = &servings[i]
	}

	out := []ClusterDeploymentDTO{}
	for i := range orders {
		order := &orders[i]
		if !selecting[order.Spec.EnvironmentRef.Name] {
			continue
		}
		out = append(out, clusterDeployment(c.Name, order, servingOf[order.Name]))
	}
	slices.SortFunc(out, func(a, b ClusterDeploymentDTO) int { return cmp.Compare(a.Order, b.Order) })
	return out
}

// clusterDeployment describes what order deploys to the cluster through its
// Serving, nil before its first promotion.
func clusterDeployment(clusterName string, order *deliveryv1alpha1.Order, serving *deliveryv1alpha1.Serving) ClusterDeploymentDTO {
	dto := ClusterDeploymentDTO{Order: order.Name, Environment: order.Spec.EnvironmentRef.Name, State: deliveryv1alpha1.ReasonPending}
	var deployed *deliveryv1alpha1.DeploymentEntry
	if serving != nil {
		deployed = serving.DeployedEntry()
	}
	if deployed == nil {
		dto.Message = "No Preparation is promoted yet"
		return dto
	}
	target := servingTarget(serving, deployed, clusterName)
	dto.Preparation = deployed.PreparationName
	dto.State, dto.Ready, dto.Message, dto.Since = target.State, target.Ready, target.Message, target.Since
	dto.ApplicationName = target.ApplicationName
	return dto
}

// servingTarget describes how the deployment of deployed by serving stands
// on the cluster clusterName: as the Serving lists it, or, for a cluster it
// does not list, healthy at the deployed revision, unless the Serving cut its
// list short or deploys nowhere yet.
func servingTarget(serving *deliveryv1alpha1.Serving, deployed *deliveryv1alpha1.DeploymentEntry, clusterName string) DeploymentTargetDTO {
	dto := DeploymentTargetDTO{Cluster: clusterName, State: stateUnknown, ApplicationName: argocd.Key(serving, clusterName).Name}
	if i := slices.IndexFunc(serving.Status.Targets, func(t deliveryv1alpha1.DeploymentTarget) bool { return t.Cluster == clusterName }); i >= 0 {
		listed := serving.Status.Targets[i]
		dto.Revision = listed.Revision
		if ready := apimeta.FindStatusCondition(listed.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil {
			dto.State, dto.Message = ready.Reason, ready.Message
			dto.Ready = ready.Status == metav1.ConditionTrue
			dto.Since = new(ready.LastTransitionTime.UTC())
		}
		return dto
	}
	counts := serving.Status.Clusters
	switch {
	case counts == nil || counts.Selected == 0:
		dto.Message = "The Serving deploys to no cluster yet"
		if ready := apimeta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady); ready != nil {
			dto.State, dto.Message = ready.Reason, ready.Message
		}
	case int(counts.Progressing+counts.Degraded+counts.Held) > len(serving.Status.Targets):
		dto.Message = fmt.Sprintf("The Serving lists %d of the %d clusters that do not run Preparation %s healthily; this one is not among them",
			len(serving.Status.Targets), counts.Progressing+counts.Degraded+counts.Held, deployed.PreparationName)
	default:
		dto.State, dto.Ready, dto.Revision = deliveryv1alpha1.ReasonHealthy, true, deployed.Digest
		dto.Message = "Runs Preparation " + deployed.PreparationName
	}
	return dto
}

// handleCreateCluster handles POST /api/v1/clusters. A cluster reached with
// a kubeconfig gets the Secret <name>-kubeconfig, written before the Cluster
// so that the Cluster never names a Secret that is missing, and owned by the
// Cluster once it exists, so that deleting the Cluster deletes it. Both are
// written as the user.
func handleCreateCluster(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req ClusterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		if req.Namespace == "" || req.Name == "" {
			respondError(w, http.StatusBadRequest, "namespace and name are required")
			return
		}
		connection := deliveryv1alpha1.ClusterConnection(cmp.Or(req.Connection, string(deliveryv1alpha1.ClusterConnectionKubeconfig)))
		kubeconfig, err := kubeconfigOf(req)
		switch {
		case err != nil:
			respondError(w, http.StatusBadRequest, err.Error())
			return
		case connection == deliveryv1alpha1.ClusterConnectionKubeconfig && kubeconfig == nil:
			respondError(w, http.StatusBadRequest, "a kubeconfig, or the server and a token, is required")
			return
		case connection == deliveryv1alpha1.ClusterConnectionInCluster && kubeconfig != nil:
			respondError(w, http.StatusBadRequest, "the cluster Kokumi runs in takes no credential")
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		c := &deliveryv1alpha1.Cluster{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    req.Labels,
			Spec:      deliveryv1alpha1.ClusterSpec{Description: req.Description, Connection: connection},
		}
		if kubeconfig != nil {
			c.Spec.Kubeconfig = &deliveryv1alpha1.ClusterKubeconfig{
				SecretRef: deliveryv1alpha1.KubeconfigSecretReference{Name: req.Name + kubeconfigSecretSuffix, Key: cluster.KubeconfigKey},
			}
		}
		// The Cluster comes first: the kubeconfig Secret of an existing
		// Cluster of that name is never written, and the Secret is owned by
		// it.
		if kubeconfig != nil {
			if err := checkCredentialSecret(r.Context(), uc, c.Namespace, c.Spec.Kubeconfig.SecretRef.Name); err != nil {
				respondSecretError(w, err)
				return
			}
		}
		if err := uc.create(r.Context(), c, clustersResource); err != nil {
			deps.logger.Error(err, "Failed to create Cluster", "namespace", req.Namespace, "name", req.Name)
			respondForbiddenOrError(w, err, "failed to create cluster")
			return
		}
		if kubeconfig != nil {
			secret := kubeconfigSecret(c, kubeconfig)
			secret.OwnerReferences = []metav1.OwnerReference{clusterOwnerReference(c)}
			if err := writeCredentialSecret(r.Context(), uc, secret); err != nil {
				deps.logger.Error(err, "Failed to store the kubeconfig of a cluster", "namespace", c.Namespace, "name", c.Name)
				respondSecretError(w, err)
				return
			}
		}

		respondJSON(w, http.StatusCreated, clusterToDTO(*c, nil, deps.installNamespace))
	}
}

// handleUpdateCluster handles PUT /api/v1/clusters/{namespace}/{name}: the
// description, the labels and, for a cluster reached with a kubeconfig the
// UI stored, optionally the credential. The connection cannot change.
func handleUpdateCluster(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req ClusterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		kubeconfig, err := kubeconfigOf(req)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		c, ok := getCluster(w, r, uc)
		if !ok {
			return
		}
		if kubeconfig != nil && c.Spec.Kubeconfig == nil {
			respondError(w, http.StatusBadRequest, "the cluster Kokumi runs in takes no credential")
			return
		}

		patch := client.MergeFrom(c.DeepCopy())
		c.Labels = req.Labels
		c.Spec.Description = req.Description
		if err := uc.patch(r.Context(), c, patch, clustersResource); err != nil {
			deps.logger.Error(err, "Failed to update Cluster", "namespace", c.Namespace, "name", c.Name)
			respondForbiddenOrError(w, err, "failed to update cluster")
			return
		}
		if kubeconfig != nil {
			secret := kubeconfigSecret(c, kubeconfig)
			secret.OwnerReferences = []metav1.OwnerReference{clusterOwnerReference(c)}
			if err := writeCredentialSecret(r.Context(), uc, secret); err != nil {
				deps.logger.Error(err, "Failed to store the kubeconfig of a cluster", "namespace", c.Namespace, "name", c.Name)
				respondSecretError(w, err)
				return
			}
		}

		respondJSON(w, http.StatusOK, clusterToDTO(*c, nil, deps.installNamespace))
	}
}

// handleDeleteCluster handles DELETE /api/v1/clusters/{namespace}/{name}. A
// kubeconfig Secret the UI stored is owned by the Cluster and goes with it.
// Kokumi creates the Cluster in-cluster of its own namespace again, as it
// does after a delete with kubectl.
func handleDeleteCluster(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		c, ok := getCluster(w, r, uc)
		if !ok {
			return
		}
		if err := uc.delete(r.Context(), c, clustersResource); err != nil {
			deps.logger.Error(err, "Failed to delete Cluster", "namespace", c.Namespace, "name", c.Name)
			respondForbiddenOrError(w, err, "failed to delete cluster")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// getCluster reads the Cluster named by the request path as uc. It writes
// the error response and reports false when it cannot be read.
func getCluster(w http.ResponseWriter, r *http.Request, uc *userClient) (*deliveryv1alpha1.Cluster, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	c := &deliveryv1alpha1.Cluster{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, c); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("cluster %s/%s not found", ns, name))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get cluster")
		return nil, false
	}
	return c, true
}

// kubeconfigOf returns the kubeconfig of a cluster request: the one it
// carries, or one built from its server, CA and token; nil when it carries
// no credential. It fails for a kubeconfig that is not self-contained.
func kubeconfigOf(req ClusterRequest) ([]byte, error) {
	var kubeconfig []byte
	switch {
	case req.Kubeconfig != "":
		kubeconfig = []byte(req.Kubeconfig)
	case req.Server != "" || req.Token != "":
		config := clientcmdapi.NewConfig()
		config.Clusters["cluster"] = &clientcmdapi.Cluster{Server: req.Server, CertificateAuthorityData: []byte(req.CAData)}
		config.AuthInfos["kokumi"] = &clientcmdapi.AuthInfo{Token: req.Token}
		config.Contexts["cluster"] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "kokumi"}
		config.CurrentContext = "cluster"
		var err error
		if kubeconfig, err = clientcmd.Write(*config); err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}
	if _, err := cluster.ParseKubeconfig(kubeconfig); err != nil {
		return nil, err
	}
	return kubeconfig, nil
}

// kubeconfigSecret returns the Secret that holds kubeconfig under the key
// the Cluster c names it by.
func kubeconfigSecret(c *deliveryv1alpha1.Cluster, kubeconfig []byte) *corev1.Secret {
	ref := c.Spec.Kubeconfig.SecretRef
	return &corev1.Secret{
		Name:      ref.Name,
		Namespace: c.Namespace,
		Type:      corev1.SecretTypeOpaque,
		Data:      map[string][]byte{cmp.Or(ref.Key, cluster.KubeconfigKey): kubeconfig},
	}
}

// clusterOwnerReference makes c the owner of its kubeconfig Secret. It does
// not block the Cluster's deletion, which would need update rights on
// clusters/finalizers.
func clusterOwnerReference(c *deliveryv1alpha1.Cluster) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         deliveryv1alpha1.GroupVersion.String(),
		Kind:               "Cluster",
		Name:               c.Name,
		UID:                c.UID,
		BlockOwnerDeletion: new(false),
	}
}
