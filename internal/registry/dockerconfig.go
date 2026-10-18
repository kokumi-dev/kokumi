package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// dockerConfig is the content of a kubernetes.io/dockerconfigjson Secret.
type dockerConfig struct {
	Auths map[string]dockerConfigEntry `json:"auths"`
}

// dockerConfigEntry is one credential of a docker config.
type dockerConfigEntry struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Auth          string `json:"auth"`
	IdentityToken string `json:"identitytoken"`
	RegistryToken string `json:"registrytoken"`
}

// credentialFor returns the credential the docker config data holds for
// loc. It matches its keys as the kubelet matches image pull Secrets: a key
// is a host, optionally with a port, a path and a scheme; "*" stands for one
// DNS label of the host; the port must be the same; and the key's path must
// be a prefix of loc's. Of the matching keys the one with the longest path
// wins, an exact host before a wildcard. The keys Docker writes for Docker
// Hub, such as https://index.docker.io/v1/, match docker.io.
func credentialFor(data []byte, loc Location) (credentials.Credential, error) {
	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return credentials.EmptyCredential, fmt.Errorf("parse docker config: %w", err)
	}

	var (
		bestKey   string
		best      dockerConfigEntry
		bestScore = -1
	)
	for key, entry := range cfg.Auths {
		score, ok := keyMatch(key, loc)
		if !ok {
			continue
		}
		// Map iteration order is random: break ties on the key, so the
		// same Secret always yields the same credential.
		if score > bestScore || (score == bestScore && key < bestKey) {
			bestKey, best, bestScore = key, entry, score
		}
	}
	if bestScore < 0 {
		return credentials.EmptyCredential, fmt.Errorf("no entry for %s", loc)
	}

	return entryCredential(best)
}

// keyMatch reports whether the docker config key matches loc and, if so,
// how specific the match is.
func keyMatch(key string, loc Location) (int, bool) {
	if !strings.Contains(key, "://") {
		key = "https://" + key
	}
	u, err := url.Parse(key)
	if err != nil || u.Host == "" {
		return 0, false
	}

	host := oci.CanonicalHost(u.Host)
	keyPath := strings.Trim(u.Path, "/")
	if host == "docker.io" && (keyPath == "v1" || keyPath == "v2") {
		// Docker writes https://index.docker.io/v1/ for Docker Hub.
		keyPath = ""
	}

	if !hostMatch(host, loc.Host) {
		return 0, false
	}
	if keyPath != "" && loc.Path != keyPath && !strings.HasPrefix(loc.Path, keyPath+"/") {
		return 0, false
	}

	// A longer path is more specific; an exact host beats a wildcard.
	score := 2 * len(keyPath)
	if !strings.Contains(host, "*") {
		score++
	}
	return score, true
}

// hostMatch reports whether the host of a key, which may hold "*" labels,
// matches host. Ports must be equal.
func hostMatch(pattern, host string) bool {
	patternHost, patternPort := splitPort(pattern)
	targetHost, targetPort := splitPort(host)
	if patternPort != targetPort {
		return false
	}

	patternLabels := strings.Split(patternHost, ".")
	targetLabels := strings.Split(targetHost, ".")
	if len(patternLabels) != len(targetLabels) {
		return false
	}
	for i := range patternLabels {
		if ok, err := path.Match(patternLabels[i], targetLabels[i]); err != nil || !ok {
			return false
		}
	}
	return true
}

// splitPort splits host[:port].
func splitPort(hostport string) (string, string) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, ""
	}
	return host, port
}

// entryCredential converts a docker config entry into a credential.
func entryCredential(entry dockerConfigEntry) (credentials.Credential, error) {
	cred := credentials.Credential{
		Username:     entry.Username,
		Password:     entry.Password,
		RefreshToken: entry.IdentityToken,
		AccessToken:  entry.RegistryToken,
	}

	if entry.Auth != "" && cred.Username == "" && cred.Password == "" {
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return credentials.EmptyCredential, fmt.Errorf("decode auth: %w", err)
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return credentials.EmptyCredential, fmt.Errorf("auth is not username:password")
		}
		cred.Username, cred.Password = user, pass
	}

	if cred == credentials.EmptyCredential {
		return credentials.EmptyCredential, fmt.Errorf("entry holds no credential")
	}
	return cred, nil
}
