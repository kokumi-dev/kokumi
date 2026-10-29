package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
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

// orderKind is the kind of the Orders the API server refuses as Invalid.
var orderKind = schema.GroupKind{Group: deliveryv1alpha1.GroupVersion.Group, Kind: "Order"}

func TestRespondForbiddenOrError(t *testing.T) {
	orders := schema.GroupResource{Group: deliveryv1alpha1.GroupVersion.Group, Resource: "orders"}
	invalid := apierrors.NewInvalid(orderKind, "web",
		field.ErrorList{field.Invalid(field.NewPath("spec", "productRef"), "shop", "productRef is immutable")})

	tests := []struct {
		name    string
		err     error
		code    int
		message string
	}{
		{name: "RBAC denial", err: apierrors.NewForbidden(orders, "web", errors.New("RBAC refuses it")), code: http.StatusForbidden},
		{name: "no mapping", err: errNoMapping, code: http.StatusForbidden},
		{name: "taken name", err: apierrors.NewAlreadyExists(orders, "web"), code: http.StatusConflict, message: `orders.delivery.kokumi.dev "web" already exists`},
		{name: "invalid spec", err: invalid, code: http.StatusUnprocessableEntity, message: "productRef is immutable"},
		{name: "stale write", err: apierrors.NewConflict(orders, "web", errors.New("the object has been modified")), code: http.StatusConflict},
		{name: "missing object", err: apierrors.NewNotFound(orders, "web"), code: http.StatusNotFound},
		{name: "wrapped status", err: fmt.Errorf("failed to get Order: %w", apierrors.NewNotFound(orders, "web")), code: http.StatusNotFound},
		{name: "the server's own credentials", err: apierrors.NewUnauthorized("token expired"), code: http.StatusInternalServerError, message: "failed to get order: "},
		{name: "API server failure", err: apierrors.NewInternalError(errors.New("etcd is down")), code: http.StatusInternalServerError},
		{name: "other error", err: errors.New("boom"), code: http.StatusInternalServerError, message: "failed to get order: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondForbiddenOrError(rec, tt.err, "failed to get order")
			assert.Equal(t, tt.code, rec.Code, rec.Body.String())
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Contains(t, body.Error, tt.message)
		})
	}
}

// TestHandlersAnswerTheAPIStatus checks that the API server's verdict on a
// write reaches the client with its status.
func TestHandlersAnswerTheAPIStatus(t *testing.T) {
	create := `{"namespace":"` + nsA + `","name":"` + webOrder + `","productRef":{"name":"web"},"environmentRef":{"name":"local"},"mode":"Manual"}`

	t.Run("a taken name is 409", func(t *testing.T) {
		backend := testBackend{registry: oci.NewFakeClient(nil), cluster: adminCluster(t)}
		rec := serveAsAdminWith(t, httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(create)), backend)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		rec = serveAsAdminWith(t, httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(create)), backend)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "already exists")
	})

	t.Run("an invalid spec is 422", func(t *testing.T) {
		cluster := fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(newOrder(webOrder, nsA)).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if review, ok := obj.(*authorizationv1.SelfSubjectAccessReview); ok {
					review.Status.Allowed = true
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
			Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
				return apierrors.NewInvalid(orderKind, obj.GetName(),
					field.ErrorList{field.Invalid(field.NewPath("spec", "promotion", "after"), webOrder, "an Order cannot run after itself")})
			},
		}).Build()
		backend := testBackend{registry: oci.NewFakeClient(nil), cluster: cluster}

		rec := serveAsAdminWith(t, httptest.NewRequest(http.MethodPut, "/api/v1/orders/"+nsA+"/"+webOrder, strings.NewReader(`{"mode":"Manual"}`)), backend)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "an Order cannot run after itself")
	})
}
