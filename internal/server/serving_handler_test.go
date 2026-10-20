package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	promoteOrder    = "web-prod"
	promoteDeployed = "web-prod-1"
	promoteQueued   = "web-prod-2"
)

// promotablePreparation returns a signed and ready Preparation of the
// promote Order, with an approval policy nobody has satisfied yet.
func promotablePreparation(name string) *deliveryv1alpha1.Preparation {
	return &deliveryv1alpha1.Preparation{
		Name: name, Namespace: nsA,
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: promoteOrder,
			Promotion: &deliveryv1alpha1.PromotionGates{
				Approvals: &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 2, AllowedGroups: []string{testGroup}},
			},
		},
		Status: deliveryv1alpha1.PreparationStatus{Conditions: []metav1.Condition{
			{Type: deliveryv1alpha1.ConditionTypeSigned, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonSigned},
			{Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonReady},
		}},
	}
}

// promoteOrderOf returns the promote Order in mode.
func promoteOrderOf(mode deliveryv1alpha1.PromotionMode) *deliveryv1alpha1.Order {
	return &deliveryv1alpha1.Order{
		Name: promoteOrder, Namespace: nsA,
		Spec: deliveryv1alpha1.OrderSpec{Promotion: &deliveryv1alpha1.PromotionSpec{Mode: mode}},
	}
}

// promote promotes preparation of the promote Order in cluster and returns
// the response.
func promote(t *testing.T, cluster client.Client, preparation string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+nsA+"/"+promoteOrder+"/promote",
		strings.NewReader(`{"preparation":"`+preparation+`"}`))
	return serveAsAdminWith(t, req, testBackend{cluster: cluster})
}

// servingSpec returns the Serving of the promote Order in cluster.
func servingSpec(t *testing.T, cluster client.Client) deliveryv1alpha1.ServingSpec {
	t.Helper()
	serving := &deliveryv1alpha1.Serving{}
	require.NoError(t, cluster.Get(context.Background(), types.NamespacedName{Namespace: nsA, Name: promoteOrder}, serving))
	return serving.Spec
}

func TestHandlePromote(t *testing.T) {
	// newCluster holds objs and allows every access review, so the user may
	// promote.
	newCluster := func(objs ...client.Object) client.Client {
		return fake.NewClientBuilder().WithScheme(unionScheme(t)).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if review, ok := obj.(*authorizationv1.SelfSubjectAccessReview); ok {
					review.Status.Allowed = true
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	}

	t.Run("queues a Preparation its approvals still hold", func(t *testing.T) {
		cluster := newCluster(promoteOrderOf(deliveryv1alpha1.PromotionModeManual), promotablePreparation(promoteQueued))

		rec := promote(t, cluster, promoteQueued)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, promoteQueued, servingSpec(t, cluster).PreparationName, "the Serving deploys it once it is approved")
	})

	t.Run("withdraws a queued promotion by promoting the deployed Preparation", func(t *testing.T) {
		serving := &deliveryv1alpha1.Serving{
			Name: promoteOrder, Namespace: nsA,
			Spec: deliveryv1alpha1.ServingSpec{OrderName: promoteOrder, PreparationName: promoteQueued},
			Status: deliveryv1alpha1.ServingStatus{History: []deliveryv1alpha1.DeploymentEntry{
				{PreparationName: promoteDeployed, PromotionRecord: "sha256:" + strings.Repeat("a", 64)},
			}},
		}
		cluster := newCluster(promoteOrderOf(deliveryv1alpha1.PromotionModeManual),
			promotablePreparation(promoteDeployed), promotablePreparation(promoteQueued), serving)

		rec := promote(t, cluster, promoteDeployed)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, promoteDeployed, servingSpec(t, cluster).PreparationName)
	})

	t.Run("rejects a Preparation that is not ready", func(t *testing.T) {
		notReady := promotablePreparation(promoteQueued)
		notReady.Status.Conditions = notReady.Status.Conditions[:1]
		cluster := newCluster(promoteOrderOf(deliveryv1alpha1.PromotionModeManual), notReady)

		rec := promote(t, cluster, promoteQueued)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	})

	t.Run("rejects an Order that promotes automatically", func(t *testing.T) {
		cluster := newCluster(promoteOrderOf(deliveryv1alpha1.PromotionModeAutomatic), promotablePreparation(promoteQueued))

		rec := promote(t, cluster, promoteQueued)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	})
}
