package argocd

import (
	"path"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ProjectAdmitsSourceNamespace reports whether the AppProject project
// admits namespace in its sourceNamespaces: whether Argo CD would let an
// Application in namespace use it. Patterns are globs, as in Argo CD.
func ProjectAdmitsSourceNamespace(project *unstructured.Unstructured, namespace string) bool {
	patterns, _, _ := unstructured.NestedStringSlice(project.Object, "spec", "sourceNamespaces")
	for _, pattern := range patterns {
		if matched, err := path.Match(pattern, namespace); err == nil && matched {
			return true
		}
	}
	return false
}
