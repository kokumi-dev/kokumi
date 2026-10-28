package argocd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/kokumi-dev/kokumi/internal/cluster"
)

const (
	testClusterNamespace = "platform"
	testServerURL        = "https://member.example:6443"

	// keyServer and keyName are the fields of a cluster Secret.
	keyServer = "server"
	keyName   = "name"
)

func testRegistration() Cluster {
	return Cluster{
		Namespace: testClusterNamespace,
		Name:      testCluster,
		Kubeconfig: cluster.Kubeconfig{
			Endpoint:   cluster.Endpoint{Server: testServerURL, CAData: []byte("ca"), TLSServerName: "member", ProxyURL: "http://proxy:3128"},
			Credential: cluster.Credential{Token: "token"},
		},
	}
}

func TestClusterSecretName(t *testing.T) {
	name := ClusterSecretName(testServerURL)
	assert.Regexp(t, `^kokumi-[0-9a-f]{16}$`, name)
	assert.Equal(t, name, ClusterSecretName(testServerURL+"/"), "a trailing slash names the same server")
	assert.NotEqual(t, name, ClusterSecretName("https://other.example:6443"))
}

func TestClusterName(t *testing.T) {
	name := ClusterName(testClusterNamespace, testCluster)
	assert.True(t, strings.HasPrefix(name, "kokumi-platform-member-"))
	assert.NotEqual(t, ClusterName("a-b", "c"), ClusterName("a", "b-c"), "the hash keeps names apart that hyphens join")
	long := ClusterName(strings.Repeat("a", 63), strings.Repeat("b", 253))
	assert.LessOrEqual(t, len(long), 253)
	assert.Empty(t, validation.IsDNS1123Subdomain(long))
}

func TestClusterSecret(t *testing.T) {
	secret, err := ClusterSecret(testRegistration())
	require.NoError(t, err)

	assert.Equal(t, Namespace, secret.Namespace)
	assert.Equal(t, ClusterSecretName(testServerURL), secret.Name, "named after its server")
	assert.Equal(t, SecretTypeCluster, secret.Labels[LabelSecretType], "Argo CD reads it as a cluster")
	namespace, owner, managed := Managed(secret)
	assert.True(t, managed)
	assert.Equal(t, testClusterNamespace, namespace)
	assert.Equal(t, testCluster, owner)
	assert.Equal(t, ClusterName(testClusterNamespace, testCluster), string(secret.Data[keyName]), "Argo CD names the cluster after the Cluster")
	assert.Equal(t, testServerURL, string(secret.Data[keyServer]))

	var config map[string]any
	require.NoError(t, json.Unmarshal(secret.Data["config"], &config))
	assert.Equal(t, map[string]any{
		"bearerToken": "token",
		"proxyUrl":    "http://proxy:3128",
		"tlsClientConfig": map[string]any{
			"insecure": false, "serverName": "member", "caData": "Y2E=",
		},
	}, config)

	t.Run("holds a client certificate", func(t *testing.T) {
		c := testRegistration()
		c.Kubeconfig.Credential = cluster.Credential{ClientCertificate: []byte("cert"), ClientKey: []byte("key")}
		secret, err := ClusterSecret(c)
		require.NoError(t, err)
		var config clusterConfig
		require.NoError(t, json.Unmarshal(secret.Data["config"], &config))
		assert.Empty(t, config.BearerToken)
		assert.Equal(t, []byte("cert"), config.TLSClientConfig.CertData)
		assert.Equal(t, []byte("key"), config.TLSClientConfig.KeyData)
	})
}

func TestConflicts(t *testing.T) {
	ours, err := ClusterSecret(testRegistration())
	require.NoError(t, err)
	other := testRegistration()
	other.Namespace = "team-a"
	theirsByKokumi, err := ClusterSecret(other)
	require.NoError(t, err)
	theirsByKokumi.Name = "kokumi-legacy"
	unmanaged := func(name, clusterName, server string) corev1.Secret {
		return corev1.Secret{
			Name: name, Labels: map[string]string{LabelSecretType: SecretTypeCluster},
			Data: map[string][]byte{keyName: []byte(clusterName), keyServer: []byte(server)},
		}
	}
	clusterName := ClusterName(testClusterNamespace, testCluster)
	repository := unmanaged("repo", clusterName, "")
	repository.Labels[LabelSecretType] = "repository"

	secrets := []corev1.Secret{
		*ours,
		unmanaged("same-server", "eu-1", testServerURL+"/"),
		unmanaged("same-name", clusterName, "https://other.example"),
		unmanaged("unrelated", "us-1", "https://us.example"),
		*theirsByKokumi,
		repository,
	}
	assert.Equal(t, []string{"same-server", "same-name"}, Conflicts(secrets, testClusterNamespace, testCluster, testServerURL),
		"only Secrets Kokumi does not manage")
}

func TestSameServer(t *testing.T) {
	assert.True(t, SameServer(testServerURL, testServerURL+"/"))
	assert.False(t, SameServer("", ""))
	assert.False(t, SameServer(testServerURL, "https://other.example:6443"))
}
