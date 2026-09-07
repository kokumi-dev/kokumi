package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Test fixtures for the union tests.
const (
	nsA      = "ns-a"
	nsB      = "ns-b"
	nsShared = "ns-shared"
	saEditor = "sa-editor"
	saOther  = "sa-other"
)

// unionScheme builds the scheme used by the union-list tests.
func unionScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, deliveryv1alpha1.AddToScheme(s))
	return s
}

// newOrder is a named Order in the given namespace.
func newOrder(name, ns string) *deliveryv1alpha1.Order {
	meta := metav1.ObjectMeta{Name: name, Namespace: ns}
	return &deliveryv1alpha1.Order{ObjectMeta: meta}
}

// newSA is a ServiceAccount fixture.
func newSA(name string) corev1.ServiceAccount {
	meta := metav1.ObjectMeta{Name: name}
	return corev1.ServiceAccount{ObjectMeta: meta}
}

// TestUserClientListUnion verifies list merges items visible to each mapped
// ServiceAccount and deduplicates objects visible to more than one SA.
func TestUserClientListUnion(t *testing.T) {
	scheme := unionScheme(t)

	// saEditor sees ns-a and the shared order, saOther sees ns-b and the
	// shared order. Fake clients don't enforce RBAC; the union logic is
	// exercised by giving each client only the objects the corresponding SA
	// could see.
	orderInA := newOrder("order-a", nsA)
	orderInB := newOrder("order-b", nsB)
	orderShared := newOrder("shared", nsShared)

	clientEditor := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orderInA, orderShared).Build()
	clientOther := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orderInB, orderShared).Build()

	imp := &impersonator{clients: map[string]client.Client{
		saEditor: clientEditor,
		saOther:  clientOther,
	}}

	uc := &userClient{imp: imp, sas: []corev1.ServiceAccount{newSA(saEditor), newSA(saOther)}}

	list := &deliveryv1alpha1.OrderList{}
	require.NoError(t, uc.list(context.Background(), list))

	got := map[types.NamespacedName]struct{}{}
	for _, o := range list.Items {
		got[types.NamespacedName{Namespace: o.Namespace, Name: o.Name}] = struct{}{}
	}
	want := map[types.NamespacedName]struct{}{
		{Namespace: nsA, Name: "order-a"}:     {},
		{Namespace: nsB, Name: "order-b"}:     {},
		{Namespace: nsShared, Name: "shared"}: {},
	}
	assert.Equal(t, want, got, "union of both SAs' visible orders, deduplicated")
}

// TestUserClientGetAnySA verifies get succeeds when any single mapped SA can
// read the object, even if the first SA cannot.
func TestUserClientGetAnySA(t *testing.T) {
	scheme := unionScheme(t)

	order := newOrder("order-b", nsB)

	clientEditor := fake.NewClientBuilder().WithScheme(scheme).Build()                   // sees nothing
	clientOther := fake.NewClientBuilder().WithScheme(scheme).WithObjects(order).Build() // sees the order

	imp := &impersonator{clients: map[string]client.Client{
		saEditor: clientEditor,
		saOther:  clientOther,
	}}

	uc := &userClient{imp: imp, sas: []corev1.ServiceAccount{newSA(saEditor), newSA(saOther)}}

	// First SA cannot see it (fake clients don't 403, they just miss); the
	// loop must continue to the second SA.
	got := &deliveryv1alpha1.Order{}
	err := uc.get(context.Background(), types.NamespacedName{Namespace: nsB, Name: "order-b"}, got)
	require.NoError(t, err)
	assert.Equal(t, "order-b", got.Name)
}
