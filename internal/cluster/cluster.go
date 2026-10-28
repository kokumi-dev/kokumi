// Package cluster reaches the clusters Orders deploy to: it reads their
// self-contained kubeconfigs, builds client configurations from them, and
// checks the health of their control planes.
package cluster

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// InCluster is the name of the Cluster of the cluster Kokumi runs in
	// that Kokumi ensures in its own namespace, named as Argo CD names that
	// cluster.
	InCluster = "in-cluster"

	// LocalEnvironment is the name of the Environment Kokumi ensures in its
	// own namespace, which selects the Cluster InCluster.
	LocalEnvironment = "local"

	// InClusterServer is the API server of the cluster Kokumi runs in, as
	// workloads in it reach it.
	InClusterServer = "https://kubernetes.default.svc"

	// KubeconfigKey is the default key of the kubeconfig in its Secret, as
	// Cluster API names it.
	KubeconfigKey = "value"
)

// Endpoint is where a cluster's API server is and how to trust it.
type Endpoint struct {
	Server        string
	CAData        []byte
	TLSServerName string
	Insecure      bool
	ProxyURL      string
}

// Credential authenticates to a cluster: a bearer token, or a client
// certificate and key.
type Credential struct {
	Token             string
	ClientCertificate []byte
	ClientKey         []byte
}

// Kubeconfig is what a self-contained kubeconfig says: where the cluster of
// its current context is, and the credential of its user.
type Kubeconfig struct {
	Endpoint   Endpoint
	Credential Credential
}

// ParseKubeconfig reads a self-contained kubeconfig, as Flux requires one:
// its current context names a cluster with a server and a user with a token
// or a client certificate and key, all inline. Exec and auth-provider users,
// file references and extensions are rejected, since nothing but the
// kubeconfig is there to resolve them.
func ParseKubeconfig(data []byte) (Kubeconfig, error) {
	config, err := clientcmd.Load(data)
	if err != nil {
		return Kubeconfig{}, fmt.Errorf("failed to read the kubeconfig: %w", err)
	}
	if len(config.Extensions) > 0 {
		return Kubeconfig{}, errors.New("kubeconfig extensions are not supported")
	}
	kubeContext, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return Kubeconfig{}, fmt.Errorf("the kubeconfig has no current-context, or no context %q", config.CurrentContext)
	}
	cluster, ok := config.Clusters[kubeContext.Cluster]
	if !ok {
		return Kubeconfig{}, fmt.Errorf("the kubeconfig has no cluster %q", kubeContext.Cluster)
	}
	user, ok := config.AuthInfos[kubeContext.AuthInfo]
	if !ok {
		return Kubeconfig{}, fmt.Errorf("the kubeconfig has no user %q", kubeContext.AuthInfo)
	}

	switch {
	case cluster.Server == "":
		return Kubeconfig{}, fmt.Errorf("cluster %q has no server", kubeContext.Cluster)
	case cluster.CertificateAuthority != "", user.ClientCertificate != "", user.ClientKey != "", user.TokenFile != "":
		return Kubeconfig{}, errors.New("file references are not supported: use certificate-authority-data, client-certificate-data, client-key-data or token")
	case user.Exec != nil || user.AuthProvider != nil:
		return Kubeconfig{}, errors.New("exec and auth-provider users are not supported: the kubeconfig must hold the credential")
	case user.Username != "" || user.Password != "":
		return Kubeconfig{}, errors.New("basic authentication is not supported: use a token or a client certificate")
	case len(user.ClientCertificateData) > 0 != (len(user.ClientKeyData) > 0):
		return Kubeconfig{}, errors.New("a client certificate needs both client-certificate-data and client-key-data")
	case user.Token == "" && len(user.ClientCertificateData) == 0:
		return Kubeconfig{}, fmt.Errorf("user %q has neither a token nor a client certificate", kubeContext.AuthInfo)
	}
	server, err := url.Parse(cluster.Server)
	if err != nil || server.Host == "" {
		return Kubeconfig{}, fmt.Errorf("the server %q of cluster %q is not an absolute URL", cluster.Server, kubeContext.Cluster)
	}

	return Kubeconfig{
		Endpoint: Endpoint{
			Server:        cluster.Server,
			CAData:        cluster.CertificateAuthorityData,
			TLSServerName: cluster.TLSServerName,
			Insecure:      cluster.InsecureSkipTLSVerify,
			ProxyURL:      cluster.ProxyURL,
		},
		Credential: Credential{
			Token:             user.Token,
			ClientCertificate: user.ClientCertificateData,
			ClientKey:         user.ClientKeyData,
		},
	}, nil
}

// Config returns the client configuration of the cluster the kubeconfig
// describes.
func (k Kubeconfig) Config() (*rest.Config, error) {
	cfg := &rest.Config{
		Host:        k.Endpoint.Server,
		BearerToken: k.Credential.Token,
		CAData:      k.Endpoint.CAData,
		ServerName:  k.Endpoint.TLSServerName,
		Insecure:    k.Endpoint.Insecure,
		CertData:    k.Credential.ClientCertificate,
		KeyData:     k.Credential.ClientKey,
	}
	if k.Endpoint.ProxyURL != "" {
		proxy, err := url.Parse(k.Endpoint.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", k.Endpoint.ProxyURL, err)
		}
		cfg.Proxy = http.ProxyURL(proxy)
	}
	return cfg, nil
}
