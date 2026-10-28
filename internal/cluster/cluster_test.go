package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	testServer = "https://member.example:6443"
	testToken  = "token"
	testMember = "member"
	testProxy  = "http://proxy.example:3128"
)

// kubeconfig returns a kubeconfig whose current context names a cluster at
// testServer and a user that edit changes.
func kubeconfig(t *testing.T, edit func(*clientcmdapi.Config)) []byte {
	t.Helper()
	config := clientcmdapi.NewConfig()
	config.Clusters[testMember] = &clientcmdapi.Cluster{Server: testServer, CertificateAuthorityData: []byte("ca")}
	config.AuthInfos["deployer"] = &clientcmdapi.AuthInfo{Token: testToken}
	config.Contexts[testMember] = &clientcmdapi.Context{Cluster: testMember, AuthInfo: "deployer"}
	config.CurrentContext = testMember
	if edit != nil {
		edit(config)
	}
	data, err := clientcmd.Write(*config)
	require.NoError(t, err)
	return data
}

func TestParseKubeconfig(t *testing.T) {
	t.Run("reads the endpoint and the token of the current context", func(t *testing.T) {
		parsed, err := ParseKubeconfig(kubeconfig(t, func(c *clientcmdapi.Config) {
			c.Clusters[testMember].TLSServerName = testMember
			c.Clusters[testMember].ProxyURL = testProxy
		}))
		require.NoError(t, err)
		assert.Equal(t, Endpoint{Server: testServer, CAData: []byte("ca"), TLSServerName: testMember, ProxyURL: testProxy}, parsed.Endpoint)
		assert.Equal(t, Credential{Token: testToken}, parsed.Credential)
	})

	t.Run("reads a client certificate", func(t *testing.T) {
		parsed, err := ParseKubeconfig(kubeconfig(t, func(c *clientcmdapi.Config) {
			c.AuthInfos["deployer"] = &clientcmdapi.AuthInfo{ClientCertificateData: []byte("cert"), ClientKeyData: []byte("key")}
		}))
		require.NoError(t, err)
		assert.Equal(t, Credential{ClientCertificate: []byte("cert"), ClientKey: []byte("key")}, parsed.Credential)
	})

	for name, edit := range map[string]func(*clientcmdapi.Config){
		"no current context":   func(c *clientcmdapi.Config) { c.CurrentContext = "" },
		"no server":            func(c *clientcmdapi.Config) { c.Clusters[testMember].Server = "" },
		"a relative server":    func(c *clientcmdapi.Config) { c.Clusters[testMember].Server = "member:6443" },
		"a CA file":            func(c *clientcmdapi.Config) { c.Clusters[testMember].CertificateAuthority = "/ca.crt" },
		"a token file":         func(c *clientcmdapi.Config) { c.AuthInfos["deployer"].TokenFile = "/token" },
		"an exec user":         func(c *clientcmdapi.Config) { c.AuthInfos["deployer"].Exec = &clientcmdapi.ExecConfig{Command: "aws"} },
		"basic authentication": func(c *clientcmdapi.Config) { c.AuthInfos["deployer"].Username = "admin" },
		"no credential":        func(c *clientcmdapi.Config) { c.AuthInfos["deployer"].Token = "" },
		"a certificate without its key": func(c *clientcmdapi.Config) {
			c.AuthInfos["deployer"] = &clientcmdapi.AuthInfo{ClientCertificateData: []byte("cert")}
		},
	} {
		t.Run("rejects a kubeconfig with "+name, func(t *testing.T) {
			_, err := ParseKubeconfig(kubeconfig(t, edit))
			assert.Error(t, err)
		})
	}

	t.Run("rejects what is no kubeconfig", func(t *testing.T) {
		_, err := ParseKubeconfig([]byte("{"))
		assert.Error(t, err)
	})
}

func TestKubeconfigConfig(t *testing.T) {
	t.Run("connects with the endpoint and the token", func(t *testing.T) {
		parsed, err := ParseKubeconfig(kubeconfig(t, func(c *clientcmdapi.Config) {
			c.Clusters[testMember].TLSServerName = testMember
		}))
		require.NoError(t, err)
		cfg, err := parsed.Config()
		require.NoError(t, err)
		assert.Equal(t, testServer, cfg.Host)
		assert.Equal(t, testToken, cfg.BearerToken)
		assert.Equal(t, []byte("ca"), cfg.CAData)
		assert.Equal(t, testMember, cfg.ServerName)
		assert.Nil(t, cfg.Proxy)
	})

	t.Run("connects through its proxy", func(t *testing.T) {
		parsed, err := ParseKubeconfig(kubeconfig(t, func(c *clientcmdapi.Config) {
			c.Clusters[testMember].ProxyURL = testProxy
		}))
		require.NoError(t, err)
		cfg, err := parsed.Config()
		require.NoError(t, err)
		assert.NotNil(t, cfg.Proxy)
	})
}
