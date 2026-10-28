package resolve

import (
	"cmp"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ClusterSelection is the clusterSelector of an Environment, parsed once to
// select many Clusters.
type ClusterSelection struct {
	namespace string
	selector  labels.Selector
}

// ClusterSelectionOf parses the clusterSelector of env.
func ClusterSelectionOf(env *deliveryv1alpha1.Environment) (ClusterSelection, error) {
	selector, err := metav1.LabelSelectorAsSelector(&env.Spec.ClusterSelector)
	if err != nil {
		return ClusterSelection{}, err
	}
	return ClusterSelection{namespace: env.Namespace, selector: selector}, nil
}

// Selects reports whether the Environment selects c: a Cluster that it
// matches and that is not being deleted.
func (s ClusterSelection) Selects(c *deliveryv1alpha1.Cluster) bool {
	return s.Matches(c) && c.DeletionTimestamp.IsZero()
}

// Matches reports whether c is a Cluster of the Environment's namespace
// whose labels match its clusterSelector, whether or not it is being
// deleted.
func (s ClusterSelection) Matches(c *deliveryv1alpha1.Cluster) bool {
	return c.Namespace == s.namespace && s.selector.Matches(labels.Set(c.Labels))
}

// SelectedClusters returns the Clusters among clusters that env selects,
// sorted by name. Every controller and the server select with it, so that
// they agree on where an Environment deploys.
func SelectedClusters(env *deliveryv1alpha1.Environment, clusters []deliveryv1alpha1.Cluster) ([]*deliveryv1alpha1.Cluster, error) {
	selection, err := ClusterSelectionOf(env)
	if err != nil {
		return nil, err
	}
	var selected []*deliveryv1alpha1.Cluster
	for i := range clusters {
		if selection.Selects(&clusters[i]) {
			selected = append(selected, &clusters[i])
		}
	}
	slices.SortFunc(selected, func(a, b *deliveryv1alpha1.Cluster) int { return cmp.Compare(a.Name, b.Name) })
	return selected, nil
}
