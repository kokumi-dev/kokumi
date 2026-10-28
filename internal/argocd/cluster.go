package argocd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
)

const (
	// LabelSecretType marks the Secrets Argo CD reads its clusters,
	// repositories and credentials from.
	LabelSecretType = "argocd.argoproj.io/secret-type"
	// SecretTypeCluster is the secret type of a cluster Secret.
	SecretTypeCluster = "cluster"

	// labelManagedBy marks the cluster Secrets Kokumi manages.
	labelManagedBy = "app.kubernetes.io/managed-by"
	managedBy      = "kokumi"

	// maxClusterNameLength is the longest name Kokumi gives a cluster in
	// Argo CD, the limit of a Secret name.
	maxClusterNameLength = 253
)

// Cluster is a cluster to register with Argo CD.
type Cluster struct {
	// Namespace and Name identify the Cluster.
	Namespace string
	Name      string
	// Kubeconfig is where the cluster is and the credential to reach it.
	Kubeconfig cluster.Kubeconfig
}

// ClusterSecretName returns the name of the cluster Secret that registers
// the API server server with Argo CD: kokumi-<hash>, where the hash is the
// first 16 hex digits of the SHA-256 of the server URL without a trailing
// slash. Argo CD holds one credential per server, and one name per server
// makes registering a server atomic: whoever creates the Secret first holds
// the server.
func ClusterSecretName(server string) string {
	sum := sha256.Sum256([]byte(strings.TrimSuffix(server, "/")))
	return "kokumi-" + hex.EncodeToString(sum[:8])
}

// ClusterName returns the name Argo CD knows the Cluster name in namespace
// by: kokumi-<namespace>-<name>-<hash>, where the hash is the first 8 hex
// digits of the SHA-256 of <namespace>/<name>, as Application keys have it.
func ClusterName(namespace, name string) string {
	return hashedName("kokumi-"+namespace+"-"+name, namespace+"/"+name, maxClusterNameLength)
}

// clusterConfig is the connection of an Argo CD cluster Secret.
type clusterConfig struct {
	BearerToken     string          `json:"bearerToken,omitempty"`
	TLSClientConfig tlsClientConfig `json:"tlsClientConfig"`
	ProxyURL        string          `json:"proxyUrl,omitempty"`
}

type tlsClientConfig struct {
	Insecure   bool   `json:"insecure"`
	ServerName string `json:"serverName,omitempty"`
	CAData     []byte `json:"caData,omitempty"`
	CertData   []byte `json:"certData,omitempty"`
	KeyData    []byte `json:"keyData,omitempty"`
}

// ClusterSecret returns the declarative cluster Secret that registers c with
// Argo CD, with the endpoint and the credential of its kubeconfig.
func ClusterSecret(c Cluster) (*corev1.Secret, error) {
	endpoint, credential := c.Kubeconfig.Endpoint, c.Kubeconfig.Credential
	config, err := json.Marshal(clusterConfig{
		BearerToken: credential.Token,
		TLSClientConfig: tlsClientConfig{
			Insecure:   endpoint.Insecure,
			ServerName: endpoint.TLSServerName,
			CAData:     endpoint.CAData,
			CertData:   credential.ClientCertificate,
			KeyData:    credential.ClientKey,
		},
		ProxyURL: endpoint.ProxyURL,
	})
	if err != nil {
		return nil, err
	}
	labels := RegistrationLabels(c.Namespace, c.Name)
	labels[LabelSecretType] = SecretTypeCluster
	return &corev1.Secret{
		Name:      ClusterSecretName(endpoint.Server),
		Namespace: Namespace,
		Labels:    labels,
		Type:      corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(ClusterName(c.Namespace, c.Name)),
			"server": []byte(endpoint.Server),
			"config": config,
		},
	}, nil
}

// RegistrationLabels returns the labels that mark the cluster Secrets
// Kokumi manages for the Cluster name in namespace, to select them by.
func RegistrationLabels(namespace, name string) map[string]string {
	return map[string]string{
		labelManagedBy:                         managedBy,
		deliveryv1alpha1.LabelCluster:          name,
		deliveryv1alpha1.LabelClusterNamespace: namespace,
	}
}

// Managed reports whether Kokumi manages the cluster Secret secret, and for
// which Cluster.
func Managed(secret *corev1.Secret) (namespace, name string, managed bool) {
	namespace, name = secret.Labels[deliveryv1alpha1.LabelClusterNamespace], secret.Labels[deliveryv1alpha1.LabelCluster]
	return namespace, name, namespace != "" && name != "" && secret.Labels[labelManagedBy] == managedBy
}

// Conflicts returns the names of the cluster Secrets among secrets that
// Kokumi does not manage and that already register server, or the name
// Argo CD knows the Cluster name in namespace by. Argo CD knows a cluster by
// either and holds one credential per server, so Kokumi registers no second
// one. Kokumi's own registrations of a server share one Secret name, which
// the API server keeps unique.
func Conflicts(secrets []corev1.Secret, namespace, name, server string) []string {
	clusterName := ClusterName(namespace, name)
	var conflicts []string
	for i := range secrets {
		secret := &secrets[i]
		if _, _, managed := Managed(secret); managed || secret.Labels[LabelSecretType] != SecretTypeCluster {
			continue
		}
		if string(secret.Data["name"]) == clusterName || SameServer(string(secret.Data["server"]), server) {
			conflicts = append(conflicts, secret.Name)
		}
	}
	return conflicts
}

// SameServer reports whether the API server URLs a and b are the same, as
// Argo CD compares them: without a trailing slash.
func SameServer(a, b string) bool {
	return a != "" && strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}
