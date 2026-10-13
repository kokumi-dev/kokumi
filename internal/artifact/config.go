package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// ConfigSchemaVersion is the schema version of Config.
const ConfigSchemaVersion = 1

// Config is the config blob of a rendered artifact. It records what was asked
// for and what the artifact was built from, so the artifact explains itself
// from the registry alone. It is authoritative over the manifest annotations
// that repeat some of its fields.
type Config struct {
	// SchemaVersion is the version of this schema.
	SchemaVersion int `json:"schemaVersion"`
	// Order identifies the Order the artifact was rendered for. Nil for
	// artifacts pre-rendered by a Menu.
	Order *ConfigObject `json:"order,omitempty"`
	// ConfigHash is the SHA-256 of Intent, byte for byte as stored.
	ConfigHash string `json:"configHash,omitempty"`
	// CommitMessage describes why the artifact was created.
	CommitMessage string `json:"commitMessage,omitempty"`
	// CreatedTime is when the artifact was rendered, in RFC 3339 UTC.
	CreatedTime string `json:"createdTime"`
	// Parent is the digest of the artifact rendered before this one for the
	// same Order. Empty for the first one.
	Parent string `json:"parent,omitempty"`
	// Source is the artifact that was rendered.
	Source ConfigSource `json:"source"`
	// Destination is where the artifact was pushed.
	Destination ConfigDestination `json:"destination"`
	// Menu identifies the Menu the configuration was merged from. Nil for
	// standalone Orders.
	Menu *ConfigMenu `json:"menu,omitempty"`
	// Renderer identifies what rendered the artifact.
	Renderer ConfigRenderer `json:"renderer"`
	// Intent is the canonical JSON of the merged configuration that
	// ConfigHash is computed from.
	Intent json.RawMessage `json:"intent,omitempty"`
}

// ConfigObject identifies a Kubernetes object.
type ConfigObject struct {
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	UID       types.UID `json:"uid,omitempty"`
}

// ConfigSource is the resolved source of a rendered artifact.
type ConfigSource struct {
	// OCI is the oci:// URL of the source repository.
	OCI string `json:"oci"`
	// Version is the tag the source was resolved from.
	Version string `json:"version"`
	// Digest is the manifest digest the tag resolved to.
	Digest string `json:"digest"`
	// PantryRef names the Pantry the source was resolved through, if any.
	// It is recorded for restore and is not part of the intent.
	PantryRef string `json:"pantryRef,omitempty"`
}

// ConfigDestination is where a rendered artifact was pushed.
type ConfigDestination struct {
	// OCI is the oci:// URL of the destination repository.
	OCI string `json:"oci"`
	// PantryRef names the Pantry the destination was resolved through, if
	// any. It is recorded for restore and is not part of the intent.
	PantryRef string `json:"pantryRef,omitempty"`
}

// ConfigMenu identifies a Menu and the state it was in.
type ConfigMenu struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// ConfigHash is the Menu's config hash when the artifact was rendered.
	ConfigHash string `json:"configHash,omitempty"`
}

// ConfigRenderer identifies what rendered an artifact.
type ConfigRenderer struct {
	// Type is "Helm" or "Manifest".
	Type string `json:"type"`
	// Version is the version of the rendering engine.
	Version string `json:"version"`
	// Builder identifies the Kokumi controller that rendered the artifact.
	Builder string `json:"builder"`
}

// ParseConfig decodes and validates a config blob of the given media type.
func ParseConfig(mediaType string, data []byte) (*Config, error) {
	if mediaType != oci.PreparationConfigMediaType {
		return nil, fmt.Errorf("unsupported config media type %q, want %q", mediaType, oci.PreparationConfigMediaType)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode artifact config: %w", err)
	}

	if cfg.SchemaVersion != ConfigSchemaVersion {
		return nil, fmt.Errorf("unsupported artifact config schema version %d", cfg.SchemaVersion)
	}

	return &cfg, nil
}

// Encode returns the JSON encoding of the config blob. Intent is written byte
// for byte, without HTML escaping, so its SHA-256 stays equal to ConfigHash.
func (c *Config) Encode() ([]byte, error) {
	var encoded bytes.Buffer

	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(c); err != nil {
		return nil, fmt.Errorf("failed to encode artifact config: %w", err)
	}

	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

// Created returns CreatedTime as a time.
func (c *Config) Created() (time.Time, error) {
	created, err := time.Parse(time.RFC3339, c.CreatedTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid createdTime %q in artifact config: %w", c.CreatedTime, err)
	}

	return created, nil
}
