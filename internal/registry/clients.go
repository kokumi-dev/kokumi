package registry

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Entry is a Registry an object lists, with the Secrets it names as its
// reader found them. Err records why they could not be read, such as a
// missing Secret or a reader that may not get it; connecting through the
// Registry fails with it.
type Entry struct {
	Registry   *deliveryv1alpha1.Registry
	Secret     *corev1.Secret
	CertSecret *corev1.Secret
	Err        error
}

// Clients builds the clients of Registries and keeps each until its
// Registry or Secrets change, so that clients keep their tokens between
// reconciles and copies within one Registry mount blobs. It reads nothing
// from the cluster: callers pass the Registries and Secrets in.
type Clients struct {
	bundledHost string
	newClient   func(oci.Options) oci.Client

	mu      sync.Mutex
	clients map[string]versionedClient
}

// versionedClient is a client built for one version of a Registry and its
// Secrets.
type versionedClient struct {
	version string
	client  oci.Client
}

// NewClients returns Clients. bundledHost is the host of the bundled
// registry, which is accessed over HTTP when no Registry covers it.
func NewClients(bundledHost string) *Clients {
	return &Clients{
		bundledHost: oci.CanonicalHost(bundledHost),
		newClient:   func(opts oci.Options) oci.Client { return oci.NewClient(opts) },
		clients:     map[string]versionedClient{},
	}
}

// WithClientFactory makes c build its clients with newClient, for tests
// that serve the registry from a fake.
func (c *Clients) WithClientFactory(newClient func(oci.Options) oci.Client) *Clients {
	c.newClient = newClient
	return c
}

// Connect returns the connection through the Registry of entry to its url.
func (c *Clients) Connect(entry Entry) (Connection, error) {
	if entry.Err != nil {
		return Connection{}, entry.Err
	}
	reg := entry.Registry
	loc, err := ParseLocation(reg.Spec.URL)
	if err != nil {
		return Connection{}, err
	}

	conn := Connection{
		Registry:  reg,
		Host:      loc.Host,
		PlainHTTP: reg.Spec.EffectiveScheme() == deliveryv1alpha1.RegistrySchemeHTTP,
	}
	version := reg.ResourceVersion

	if ref := reg.Spec.SecretRef; ref != nil {
		secret := entry.Secret
		if secret == nil {
			return Connection{}, SecretNotFound(ref.Name)
		}
		if secret.Type != corev1.SecretTypeDockerConfigJson {
			return Connection{}, &Error{Reason: deliveryv1alpha1.ReasonSecretInvalid,
				Err: fmt.Errorf("secret %q is of type %s, not %s", ref.Name, secret.Type, corev1.SecretTypeDockerConfigJson)}
		}
		conn.Credential, err = credentialFor(secret.Data[corev1.DockerConfigJsonKey], loc)
		if err != nil {
			return Connection{}, &Error{Reason: deliveryv1alpha1.ReasonSecretInvalid, Err: fmt.Errorf("secret %q: %w", ref.Name, err)}
		}
		version += "/" + secret.ResourceVersion
	}

	if ref := reg.Spec.CertSecretRef; ref != nil {
		secret := entry.CertSecret
		if secret == nil {
			return Connection{}, SecretNotFound(ref.Name)
		}
		conn.TLSConfig, err = tlsConfig(secret)
		if err != nil {
			return Connection{}, &Error{Reason: deliveryv1alpha1.ReasonCertificateInvalid, Err: fmt.Errorf("secret %q: %w", ref.Name, err)}
		}
		version += "/" + secret.ResourceVersion
	}

	conn.version = version
	conn.Client = c.client(string(reg.UID), version, oci.Options{
		Host:       loc.Host,
		Credential: conn.Credential,
		PlainHTTP:  conn.PlainHTTP,
		TLSConfig:  conn.TLSConfig,
	})
	return conn, nil
}

// Scope returns the Scope of an object that lists the Registries of
// entries in its registryRefs.
func (c *Clients) Scope(entries []Entry) Scope {
	return Scope{clients: c, entries: entries}
}

// anonymous returns the anonymous connection to loc.
func (c *Clients) anonymous(loc Location) Connection {
	plainHTTP := loc.Host == c.bundledHost
	return Connection{
		Host:      loc.Host,
		PlainHTTP: plainHTTP,
		Client:    c.client(fmt.Sprintf("anonymous/%t", plainHTTP), "", oci.Options{PlainHTTP: plainHTTP}),
	}
}

// client returns the client cached for the Registry with the given UID at
// version, building it from opts when there is none. A version names one
// version of the Registry and its Secrets: a change replaces the client.
func (c *Clients) client(uid, version string, opts oci.Options) oci.Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.clients[uid]; ok && cached.version == version {
		return cached.client
	}
	client := c.newClient(opts)
	c.clients[uid] = versionedClient{version: version, client: client}
	return client
}

// Scope is the Registries an object connects through: the ones its
// registryRefs name in its namespace. The zero Scope connects nowhere: its
// connections carry no client, and callers use their default client.
type Scope struct {
	clients *Clients
	entries []Entry
	// static holds the clients of a StaticScope by canonical host.
	static map[string]oci.Client
}

// StaticScope returns a Scope that connects to each host in clients
// anonymously with its client, and to every other host without a client.
// It serves tests that fake registries.
func StaticScope(clients map[string]oci.Client) Scope {
	static := make(map[string]oci.Client, len(clients))
	for host, c := range clients {
		static[oci.CanonicalHost(host)] = c
	}
	return Scope{static: static}
}

// Connect returns the connection to url: through the Registry whose url
// covers it with the longest path, anonymously when none does.
func (s Scope) Connect(url string) (Connection, error) {
	loc, err := ParseLocation(url)
	if err != nil {
		return Connection{}, err
	}
	if s.static != nil {
		return Connection{Host: loc.Host, Client: s.static[loc.Host]}, nil
	}
	if s.clients == nil {
		return Connection{}, nil
	}

	reg, err := Match(s.Registries(), url)
	if err != nil {
		return Connection{}, err
	}
	if reg == nil {
		return s.clients.anonymous(loc), nil
	}
	for _, entry := range s.entries {
		if entry.Registry.Name != reg.Name {
			continue
		}
		conn, err := s.clients.Connect(entry)
		if err != nil {
			return Connection{}, fmt.Errorf("registry %q: %w", reg.Name, err)
		}
		return conn, nil
	}
	return Connection{}, fmt.Errorf("registry %q: not in the scope", reg.Name)
}

// Registries returns the Registries of the Scope, to Match locations
// against.
func (s Scope) Registries() []deliveryv1alpha1.Registry {
	registries := make([]deliveryv1alpha1.Registry, 0, len(s.entries))
	for _, entry := range s.entries {
		registries = append(registries, *entry.Registry)
	}
	return registries
}

// Private reports whether ref, read through conn, is private: conn
// identifies Kokumi to the registry, and the registry refuses to resolve
// ref without that. Content read through a Registry that also serves it to
// anyone, such as public images read with credentials for higher rate
// limits, is not private.
func (s Scope) Private(ctx context.Context, conn Connection, ref oci.Reference) bool {
	if s.clients == nil || conn.Registry == nil || !conn.Authenticated() {
		return false
	}

	var tlsConfig *tls.Config
	if conn.TLSConfig != nil {
		tlsConfig = conn.TLSConfig.Clone()
		tlsConfig.Certificates = nil
	}
	anonymous := s.clients.client(string(conn.Registry.UID)+"/anonymous", conn.version, oci.Options{
		PlainHTTP: conn.PlainHTTP,
		TLSConfig: tlsConfig,
	})

	_, err := anonymous.Resolve(ctx, ref)
	return err != nil
}
