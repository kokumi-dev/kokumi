package artifact_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

func TestPipeline_Render_config(t *testing.T) {
	const parent = "sha256:c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1"

	render := func(t *testing.T, mutate func(*artifact.RenderRequest)) (*oci.FakeClient, *artifact.RenderResult) {
		t.Helper()

		fs := afero.NewMemMapFs()
		client := oci.NewFakeClient(fs)
		p := artifact.NewPipeline(artifact.NewStore(client, fs, ""))

		req := baseRenderRequest()
		mutate(&req)

		result, err := p.Render(context.Background(), req)
		require.NoError(t, err)

		return client, result
	}

	t.Run("round trips through the config blob", func(t *testing.T) {
		before := time.Now().UTC().Truncate(time.Second)

		client, result := render(t, func(req *artifact.RenderRequest) {
			req.Description = "bump replicas to 3"
			req.ParentDigest = parent
			req.ArtifactType = oci.PreparationArtifactType
			req.Config = artifact.Config{
				Order:       &artifact.ConfigObject{Namespace: "shop", Name: "podinfo-prod", UID: "6f0c"},
				ConfigHash:  "sha256:9b1d",
				Source:      artifact.ConfigSource{PantryRef: "upstream"},
				Destination: artifact.ConfigDestination{PantryRef: "internal"},
				Menu:        &artifact.ConfigMenu{Namespace: "shop", Name: "podinfo", ConfigHash: "sha256:77e0"},
				Renderer:    artifact.ConfigRenderer{Type: "Manifest", Version: "v0.17.0", Builder: "kokumi@0.17.0"},
				Intent:      json.RawMessage(`{"destination":"oci://dest","source":{"oci":"oci://src","version":"1.0.0"}}`),
			}
		})

		require.Len(t, client.Pushes, 1)
		pushed := client.Pushes[0].Options
		require.Equal(t, oci.PreparationArtifactType, pushed.ArtifactType)
		require.Equal(t, oci.PreparationConfigMediaType, pushed.ConfigMediaType)

		got, err := artifact.ParseConfig(pushed.ConfigMediaType, pushed.Config)
		require.NoError(t, err)
		require.Equal(t, &result.Config, got)

		require.Equal(t, artifact.ConfigSchemaVersion, got.SchemaVersion)
		require.Equal(t, "bump replicas to 3", got.CommitMessage)
		require.Equal(t, parent, got.Parent)
		require.Equal(t, artifact.ConfigSource{
			OCI:       result.SourceRef.OCIRepositoryReference(),
			Version:   testVersion,
			Digest:    result.SourceRef.Digest,
			PantryRef: "upstream",
		}, got.Source)
		require.Equal(t, artifact.ConfigDestination{OCI: result.DestRef.OCIRepositoryReference(), PantryRef: "internal"}, got.Destination)
		require.JSONEq(t, `{"destination":"oci://dest","source":{"oci":"oci://src","version":"1.0.0"}}`, string(got.Intent))

		created, err := got.Created()
		require.NoError(t, err)
		require.False(t, created.Before(before))

		t.Run("annotations repeat the config", func(t *testing.T) {
			require.Equal(t, parent, pushed.Annotations[oci.AnnotationParentDigest])
			require.Equal(t, "bump replicas to 3", pushed.Annotations["org.opencontainers.image.description"])
			require.Equal(t, got.CreatedTime, pushed.Annotations["org.opencontainers.image.created"])
		})
	})

	t.Run("stores the intent byte for byte", func(t *testing.T) {
		intent := []byte(`{"render":{"helm":{"values":{"cmd":"a && b","tpl":"<b>"}}}}`)

		client, _ := render(t, func(req *artifact.RenderRequest) {
			req.Config = artifact.Config{Intent: intent}
		})

		pushed := client.Pushes[0].Options
		got, err := artifact.ParseConfig(pushed.ConfigMediaType, pushed.Config)
		require.NoError(t, err)
		require.Equal(t, string(intent), string(got.Intent))
	})

	t.Run("a pinned source digest keeps the version as the pushed tag", func(t *testing.T) {
		_, result := render(t, func(req *artifact.RenderRequest) {
			req.Source.Digest = parent
		})

		require.Equal(t, testVersion, result.Config.Source.Version)
		require.Equal(t, testVersion, result.DestRef.Tag)
	})
}

func TestParseConfig(t *testing.T) {
	t.Run("rejects a foreign media type", func(t *testing.T) {
		_, err := artifact.ParseConfig("application/vnd.oci.empty.v1+json", []byte("{}"))
		require.Error(t, err)
	})

	t.Run("rejects an unknown schema version", func(t *testing.T) {
		_, err := artifact.ParseConfig(oci.PreparationConfigMediaType, []byte(`{"schemaVersion":2}`))
		require.Error(t, err)
	})
}
