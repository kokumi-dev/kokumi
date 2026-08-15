package oci

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestFakeClient_PushDigest(t *testing.T) {
	ctx := context.Background()
	client := NewFakeClient(afero.NewMemMapFs())

	a, err := client.Push(ctx, "registry.example/a", "1.0.0", "/out", nil)
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, a)

	b, err := client.Push(ctx, "registry.example/a", "1.0.0", "/out", nil)
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, b)
	require.NotEqual(t, a, b)
}
