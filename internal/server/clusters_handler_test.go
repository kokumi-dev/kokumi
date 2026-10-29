package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/cluster"
)

// Values the cluster tests share.
const (
	memberServer = "https://member.example:6443"
	member       = "member"
	edge         = "edge"
	fleet        = "fleet"
	prod         = "prod"
	shopNS       = "shop"
	webOrder     = "web"
	kokumiNS     = "kokumi"
	platformNS   = "platform"
	// ghcr, ghcrURL and ghcrPassword are the Registry the registry tests
	// save.
	ghcr         = "ghcr"
	ghcrURL      = "oci://ghcr.io/kokumi-dev"
	ghcrPassword = "secret"
)

func TestKubeconfigOf(t *testing.T) {
	t.Run("builds a kubeconfig of the server, its CA and a token", func(t *testing.T) {
		kubeconfig, err := kubeconfigOf(ClusterRequest{Server: memberServer, CAData: "ca", Token: "token"})
		require.NoError(t, err)
		parsed, err := cluster.ParseKubeconfig(kubeconfig)
		require.NoError(t, err)
		assert.Equal(t, memberServer, parsed.Endpoint.Server)
		assert.Equal(t, []byte("ca"), parsed.Endpoint.CAData)
		assert.Equal(t, "token", parsed.Credential.Token)
	})

	t.Run("carries no credential without one", func(t *testing.T) {
		kubeconfig, err := kubeconfigOf(ClusterRequest{Name: member})
		require.NoError(t, err)
		assert.Nil(t, kubeconfig)
	})

	t.Run("refuses a kubeconfig that is not self-contained", func(t *testing.T) {
		_, err := kubeconfigOf(ClusterRequest{Server: memberServer})
		assert.ErrorContains(t, err, "neither a token nor a client certificate")
		_, err = kubeconfigOf(ClusterRequest{Kubeconfig: "{"})
		assert.Error(t, err)
	})
}

func TestClustersToDTO(t *testing.T) {
	spoke := deliveryv1alpha1.Cluster{
		Name: member, Namespace: platformNS, Labels: map[string]string{fleet: edge},
		Spec: deliveryv1alpha1.ClusterSpec{
			Connection: deliveryv1alpha1.ClusterConnectionKubeconfig,
			Kubeconfig: &deliveryv1alpha1.ClusterKubeconfig{SecretRef: deliveryv1alpha1.KubeconfigSecretReference{Name: "member-kubeconfig"}},
		},
		Status: deliveryv1alpha1.ClusterStatus{
			Server: memberServer, KubernetesVersion: "v1.36.2",
			Conditions: []metav1.Condition{{
				Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonUnreachable, Message: "down",
			}},
		},
	}
	inCluster := deliveryv1alpha1.Cluster{
		Name: cluster.InCluster, Namespace: kokumiNS, Labels: map[string]string{deliveryv1alpha1.LabelInCluster: "true"},
		Spec: deliveryv1alpha1.ClusterSpec{Connection: deliveryv1alpha1.ClusterConnectionInCluster},
	}
	const everything = "all"
	environments := []deliveryv1alpha1.Environment{
		{Name: prod, Namespace: platformNS, Spec: deliveryv1alpha1.EnvironmentSpec{ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{fleet: edge}}}},
		{Name: everything, Namespace: platformNS},
		{Name: everything, Namespace: "elsewhere"},
	}

	clusters := clustersToDTO([]deliveryv1alpha1.Cluster{spoke, inCluster}, environments, kokumiNS)
	require.Len(t, clusters, 2)
	assert.Equal(t, []string{kokumiNS, platformNS}, []string{clusters[0].Namespace, clusters[1].Namespace}, "sorted by namespace")

	assert.True(t, clusters[0].Ensured, "Kokumi keeps in-cluster in its own namespace")
	assert.Equal(t, "InCluster", clusters[0].Connection)
	assert.Equal(t, "Unknown", clusters[0].State)
	assert.Empty(t, clusters[0].Environments)

	assert.False(t, clusters[1].Ensured)
	assert.Equal(t, "member-kubeconfig", clusters[1].KubeconfigSecret)
	assert.Equal(t, memberServer, clusters[1].Server)
	assert.Equal(t, "v1.36.2", clusters[1].KubernetesVersion)
	assert.Equal(t, deliveryv1alpha1.ReasonUnreachable, clusters[1].State)
	assert.False(t, clusters[1].Ready)
	assert.Equal(t, "down", clusters[1].Message)
	assert.Equal(t, []string{everything, prod}, clusters[1].Environments, "only the Environments of its namespace")
}

func TestEnvironmentToDTO(t *testing.T) {
	spec := deliveryv1alpha1.EnvironmentSpec{
		ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{fleet: prod}},
		Deployer: deliveryv1alpha1.Deployer{
			Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: argocd.DefaultProject},
		},
		Promotion: &deliveryv1alpha1.EnvironmentPromotion{RequireAfter: []deliveryv1alpha1.RequiredEnvironment{{Environment: "staging", MinSoakSeconds: 60}}},
	}
	env := deliveryv1alpha1.Environment{Name: prod, Namespace: platformNS, Spec: spec}
	env.Status.Clusters = &deliveryv1alpha1.EnvironmentClusters{Selected: 2, Ready: 1}

	dto := environmentToDTO(env, kokumiNS)
	assert.Equal(t, platformNS, dto.Namespace)
	assert.Equal(t, spec, dto.EnvironmentSpec, "the spec as the API has it")
	assert.Equal(t, int32(2), dto.SelectedClusters)
	assert.Equal(t, int32(1), dto.ReadyClusters)
	assert.False(t, dto.Ensured)
	data, err := json.Marshal(dto)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"deployer":{"type":"ArgoCD","argoCD":{"project":"default"}}`, "flat, in the API's field names")

	local := deliveryv1alpha1.Environment{Name: cluster.LocalEnvironment, Namespace: kokumiNS}
	assert.True(t, environmentToDTO(local, kokumiNS).Ensured, "Kokumi keeps local in its own namespace")
}

func TestHandleCreateEnvironmentRefusesAnInvalidSelector(t *testing.T) {
	admin := adminCluster(t)
	rec := serveClusters(t, http.MethodPost, "/api/v1/environments", EnvironmentRequest{
		Namespace: platformNS, Name: prod,
		ClusterSelector: metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: fleet, Operator: metav1.LabelSelectorOpIn}},
		},
	}, admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "invalid clusterSelector")
}

func TestHandleListClustersBySelector(t *testing.T) {
	labeled := func(name, value string) *deliveryv1alpha1.Cluster {
		return &deliveryv1alpha1.Cluster{Name: name, Namespace: platformNS, Labels: map[string]string{fleet: value}}
	}
	deleting := labeled("leaving", prod)
	deleting.Finalizers = []string{deliveryv1alpha1.Finalizer}
	deleting.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	admin := adminCluster(t, labeled("eu", prod), labeled("us", edge), deleting)

	rec := serveClusters(t, http.MethodGet, "/api/v1/clusters?namespace="+platformNS+"&labelSelector="+fleet+"%3D"+prod, nil, admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var clusters []ClusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &clusters))
	require.Len(t, clusters, 1, "what an Environment with the selector selects: not the one being deleted")
	assert.Equal(t, "eu", clusters[0].Name)

	rec = serveClusters(t, http.MethodGet, "/api/v1/clusters?labelSelector=%3D%3D", nil, admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestServingToDTOClusters(t *testing.T) {
	serving := deliveryv1alpha1.Serving{Name: "storefront", Namespace: shopNS, Spec: deliveryv1alpha1.ServingSpec{OrderName: "storefront"}}
	serving.Status.Clusters = &deliveryv1alpha1.DeploymentClusters{Selected: 3, Healthy: 2, Progressing: 1}

	assert.Equal(t, &DeploymentClustersDTO{Selected: 3, Healthy: 2, Progressing: 1}, servingToDTO(serving).Clusters,
		"the list sends counts; the targets come with /targets")
}

// serveClusters serves a request to path with body, as JSON, as the admin
// against cluster.
func serveClusters(t *testing.T, method, path string, body any, k8s client.Client) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	return serveAsAdminWith(t, httptest.NewRequest(method, path, reader), testBackend{cluster: k8s})
}

// storedKubeconfig returns the kubeconfig Secret of the Cluster member in
// the platform namespace of k8s.
func storedKubeconfig(t *testing.T, k8s client.Client) (*corev1.Secret, error) {
	t.Helper()
	secret := &corev1.Secret{}
	err := k8s.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: member + kubeconfigSecretSuffix}, secret)
	return secret, err
}

// kubeconfigCluster returns the Cluster member in the platform namespace,
// reached with the kubeconfig in its Secret.
func kubeconfigCluster() *deliveryv1alpha1.Cluster {
	return &deliveryv1alpha1.Cluster{
		Name: member, Namespace: platformNS,
		Spec: deliveryv1alpha1.ClusterSpec{
			Connection: deliveryv1alpha1.ClusterConnectionKubeconfig,
			Kubeconfig: &deliveryv1alpha1.ClusterKubeconfig{
				SecretRef: deliveryv1alpha1.KubeconfigSecretReference{Name: member + kubeconfigSecretSuffix, Key: cluster.KubeconfigKey},
			},
		},
	}
}

// managedKubeconfig returns the kubeconfig Secret of the Cluster member that
// the UI stored, holding value.
func managedKubeconfig(value string) *corev1.Secret {
	return &corev1.Secret{
		Name: member + kubeconfigSecretSuffix, Namespace: platformNS,
		Labels: map[string]string{labelManagedBy: managedByKokumi},
		Data:   map[string][]byte{cluster.KubeconfigKey: []byte(value)},
	}
}

func TestHandleCreateCluster(t *testing.T) {
	kubeconfig, err := kubeconfigOf(ClusterRequest{Server: memberServer, Token: "token"})
	require.NoError(t, err)
	request := ClusterRequest{Namespace: platformNS, Name: member, Kubeconfig: string(kubeconfig)}

	t.Run("stores the kubeconfig in a Secret the Cluster owns", func(t *testing.T) {
		admin := adminCluster(t)

		rec := serveClusters(t, http.MethodPost, "/api/v1/clusters", request, admin)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		c := &deliveryv1alpha1.Cluster{}
		require.NoError(t, admin.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: member}, c))
		assert.Equal(t, member+kubeconfigSecretSuffix, c.Spec.Kubeconfig.SecretRef.Name)
		secret, err := storedKubeconfig(t, admin)
		require.NoError(t, err)
		assert.Equal(t, kubeconfig, secret.Data[cluster.KubeconfigKey])
		assert.Equal(t, managedByKokumi, secret.Labels[labelManagedBy])
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, "Cluster", secret.OwnerReferences[0].Kind)
		assert.Equal(t, member, secret.OwnerReferences[0].Name)
	})

	t.Run("stores no Secret when the Cluster cannot be created", func(t *testing.T) {
		admin := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				switch o := obj.(type) {
				case *authorizationv1.SelfSubjectAccessReview:
					o.Status.Allowed = true
					return nil
				case *deliveryv1alpha1.Cluster:
					return apierrors.NewForbidden(schema.GroupResource{Group: deliveryv1alpha1.GroupVersion.Group, Resource: clustersResource}, o.Name,
						errors.New("the cluster-secret-access policy refuses it"))
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

		rec := serveClusters(t, http.MethodPost, "/api/v1/clusters", request, admin)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		_, err := storedKubeconfig(t, admin)
		assert.True(t, apierrors.IsNotFound(err), "no Secret outlives a Cluster that was not created")
	})

	t.Run("refuses a Cluster that exists, and leaves its Secret alone", func(t *testing.T) {
		admin := adminCluster(t, kubeconfigCluster(), managedKubeconfig("stored"))

		rec := serveClusters(t, http.MethodPost, "/api/v1/clusters", request, admin)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		secret, err := storedKubeconfig(t, admin)
		require.NoError(t, err)
		assert.Equal(t, "stored", string(secret.Data[cluster.KubeconfigKey]))
	})

	t.Run("refuses a Secret of that name Kokumi did not create", func(t *testing.T) {
		foreign := managedKubeconfig("foreign")
		foreign.Labels = nil
		admin := adminCluster(t, foreign)

		rec := serveClusters(t, http.MethodPost, "/api/v1/clusters", request, admin)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		err := admin.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: member}, &deliveryv1alpha1.Cluster{})
		assert.True(t, apierrors.IsNotFound(err))
	})
}

func TestHandleUpdateCluster(t *testing.T) {
	admin := adminCluster(t, kubeconfigCluster(), managedKubeconfig("stored"))

	rec := serveClusters(t, http.MethodPut, "/api/v1/clusters/"+platformNS+"/"+member,
		ClusterRequest{Description: "Production in the EU", Labels: map[string]string{fleet: prod}}, admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	c := &deliveryv1alpha1.Cluster{}
	require.NoError(t, admin.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: member}, c))
	assert.Equal(t, "Production in the EU", c.Spec.Description)
	assert.Equal(t, map[string]string{fleet: prod}, c.Labels)
	secret, err := storedKubeconfig(t, admin)
	require.NoError(t, err)
	assert.Equal(t, "stored", string(secret.Data[cluster.KubeconfigKey]), "an update without a credential keeps the stored one")
}

func TestHandleDeleteCluster(t *testing.T) {
	inCluster := &deliveryv1alpha1.Cluster{
		Name: cluster.InCluster, Namespace: capsInstallNamespace,
		Spec: deliveryv1alpha1.ClusterSpec{Connection: deliveryv1alpha1.ClusterConnectionInCluster},
	}
	admin := adminCluster(t, inCluster)

	rec := serveClusters(t, http.MethodDelete, "/api/v1/clusters/"+capsInstallNamespace+"/"+cluster.InCluster, nil, admin)
	assert.Equal(t, http.StatusNoContent, rec.Code, "Kokumi creates it again, as after a delete with kubectl: %s", rec.Body.String())
}

func TestClusterDeployments(t *testing.T) {
	const deployed = "web-1"
	record := "sha256:" + strings.Repeat("a", 64)
	target := &deliveryv1alpha1.Cluster{Name: member, Namespace: platformNS, Labels: map[string]string{fleet: prod}}
	environments := []deliveryv1alpha1.Environment{
		{Name: prod, Namespace: platformNS, Spec: deliveryv1alpha1.EnvironmentSpec{
			ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{fleet: prod}},
		}},
		{Name: edge, Namespace: platformNS, Spec: deliveryv1alpha1.EnvironmentSpec{
			ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{fleet: edge}},
		}},
	}
	order := func(name, environment string) deliveryv1alpha1.Order {
		return deliveryv1alpha1.Order{Name: name, Namespace: platformNS, Spec: deliveryv1alpha1.OrderSpec{
			EnvironmentRef: deliveryv1alpha1.EnvironmentRef{Name: environment},
		}}
	}
	serving := func(name string, counts deliveryv1alpha1.DeploymentClusters, targets ...deliveryv1alpha1.DeploymentTarget) deliveryv1alpha1.Serving {
		return deliveryv1alpha1.Serving{
			Name: name, Namespace: platformNS, Spec: deliveryv1alpha1.ServingSpec{OrderName: name},
			Status: deliveryv1alpha1.ServingStatus{
				History:  []deliveryv1alpha1.DeploymentEntry{{PreparationName: deployed, PromotionRecord: record}},
				Clusters: &counts,
				Targets:  targets,
			},
		}
	}
	degraded := deliveryv1alpha1.DeploymentTarget{Cluster: member, Conditions: []metav1.Condition{{
		Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonDegraded, Message: "crash loop",
	}}}

	got := clusterDeployments(target, environments,
		[]deliveryv1alpha1.Order{order(webOrder, prod), order(shopNS, prod), order("blog", prod), order("cdn", edge)},
		[]deliveryv1alpha1.Serving{
			serving(webOrder, deliveryv1alpha1.DeploymentClusters{Selected: 2, Healthy: 1, Degraded: 1}, degraded),
			serving(shopNS, deliveryv1alpha1.DeploymentClusters{Selected: 2, Healthy: 2}),
		})

	require.Len(t, got, 3, "only the Orders of the Environments that select the cluster")
	assert.Equal(t, []string{"blog", shopNS, webOrder}, []string{got[0].Order, got[1].Order, got[2].Order})
	assert.Equal(t, deliveryv1alpha1.ReasonPending, got[0].State, "not promoted yet")
	assert.Empty(t, got[0].ApplicationName)
	assert.Equal(t, deliveryv1alpha1.ReasonHealthy, got[1].State, "a cluster the Serving does not list runs it healthily")
	assert.True(t, got[1].Ready)
	assert.Equal(t, deliveryv1alpha1.ReasonDegraded, got[2].State)
	assert.Equal(t, "crash loop", got[2].Message)
	shop := serving(shopNS, deliveryv1alpha1.DeploymentClusters{})
	assert.Equal(t, argocd.Key(&shop, member).Name, got[1].ApplicationName, "the UI links the Application Kokumi named")
}

func TestEffectiveTargetNamespace(t *testing.T) {
	order := &deliveryv1alpha1.Order{Name: webOrder, Namespace: shopNS}
	product := &deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{TargetNamespace: "webshop"}}

	namespace, source := effectiveTargetNamespace(order, nil)
	assert.Equal(t, [2]string{shopNS, targetNamespaceFromNamespace}, [2]string{namespace, source}, "without a Product, the Order's own")
	namespace, source = effectiveTargetNamespace(order, product)
	assert.Equal(t, [2]string{"webshop", targetNamespaceFromProduct}, [2]string{namespace, source})
	order.Spec.TargetNamespace = webOrder
	namespace, source = effectiveTargetNamespace(order, product)
	assert.Equal(t, [2]string{webOrder, targetNamespaceFromOrder}, [2]string{namespace, source})
}

func TestHandleCreateRegistryStoresNoSecretWithoutItsRegistry(t *testing.T) {
	admin := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			switch o := obj.(type) {
			case *authorizationv1.SelfSubjectAccessReview:
				o.Status.Allowed = true
				return nil
			case *deliveryv1alpha1.Registry:
				return apierrors.NewForbidden(schema.GroupResource{Group: deliveryv1alpha1.GroupVersion.Group, Resource: registriesResource}, o.Name,
					errors.New("the registry-secret-access policy refuses it"))
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()

	rec := serveClusters(t, http.MethodPost, "/api/v1/registries", CreateRegistryRequest{
		Namespace: platformNS, Name: ghcr, URL: ghcrURL, Username: "robot", Password: ghcrPassword,
	}, admin)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	err := admin.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: ghcr + credentialsSecretSuffix}, &corev1.Secret{})
	assert.True(t, apierrors.IsNotFound(err), "no Secret outlives a Registry that was not created")
}

func TestHandleUpdateRegistryStoresTheSecretAfterItsRegistry(t *testing.T) {
	registry := func() *deliveryv1alpha1.Registry {
		return &deliveryv1alpha1.Registry{Name: ghcr, Namespace: platformNS, Spec: deliveryv1alpha1.RegistrySpec{URL: ghcrURL}}
	}
	request := UpdateRegistryRequest{URL: ghcrURL, Username: "robot", Password: ghcrPassword}
	path := "/api/v1/registries/" + platformNS + "/" + ghcr
	secretKey := types.NamespacedName{Namespace: platformNS, Name: ghcr + credentialsSecretSuffix}

	t.Run("stores the credentials in a Secret the Registry owns", func(t *testing.T) {
		admin := adminCluster(t, registry())

		rec := serveClusters(t, http.MethodPut, path, request, admin)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		secret := &corev1.Secret{}
		require.NoError(t, admin.Get(context.Background(), secretKey, secret))
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, "Registry", secret.OwnerReferences[0].Kind)
	})

	t.Run("stores no Secret when the Registry cannot be patched", func(t *testing.T) {
		admin := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(registry()).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if review, ok := obj.(*authorizationv1.SelfSubjectAccessReview); ok {
					review.Status.Allowed = true
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*deliveryv1alpha1.Registry); ok {
					return apierrors.NewForbidden(schema.GroupResource{Group: deliveryv1alpha1.GroupVersion.Group, Resource: registriesResource}, obj.GetName(),
						errors.New("the registry-secret-access policy refuses it"))
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		rec := serveClusters(t, http.MethodPut, path, request, admin)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		err := admin.Get(context.Background(), secretKey, &corev1.Secret{})
		assert.True(t, apierrors.IsNotFound(err), "a refused edit stores no Secret")
	})

	t.Run("refuses a Secret Kokumi did not create, and leaves the Registry as it was", func(t *testing.T) {
		foreign := &corev1.Secret{Name: secretKey.Name, Namespace: platformNS}
		admin := adminCluster(t, registry(), foreign)

		rec := serveClusters(t, http.MethodPut, path, request, admin)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		stored := &deliveryv1alpha1.Registry{}
		require.NoError(t, admin.Get(context.Background(), types.NamespacedName{Namespace: platformNS, Name: ghcr}, stored))
		assert.Nil(t, stored.Spec.SecretRef, "the Registry does not name the foreign Secret")
	})
}
