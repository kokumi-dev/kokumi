package argocd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestProjectAdmitsSourceNamespace(t *testing.T) {
	withSources := func(namespaces ...any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"sourceNamespaces": namespaces}}}
	}
	assert.True(t, ProjectAdmitsSourceNamespace(withSources("shop"), "shop"))
	assert.True(t, ProjectAdmitsSourceNamespace(withSources("team-*"), "team-a"))
	assert.True(t, ProjectAdmitsSourceNamespace(withSources("*"), "anything"))
	assert.True(t, ProjectAdmitsSourceNamespace(withSources("team-[ab]"), "team-b"))
	assert.False(t, ProjectAdmitsSourceNamespace(withSources("payments"), "shop"))
	assert.False(t, ProjectAdmitsSourceNamespace(withSources("[invalid"), "shop"), "a pattern that does not parse admits none")
	assert.False(t, ProjectAdmitsSourceNamespace(withSources(), "shop"), "no sourceNamespaces admits none")
}
