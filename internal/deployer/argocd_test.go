package deployer

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/event"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testSyncSynced   = "Synced"
	testOrderName    = "my-order"
	testHealthStatus = "Healthy"
)

// appStatus builds an Application status map as the Argo CD Application
// controller would write it for a synced, healthy application at the given
// revision.
func appStatus(revision string) map[string]any {
	return map[string]any{
		"sync":   map[string]any{"status": testSyncSynced, "revision": revision},
		"health": map[string]any{"status": testHealthStatus},
	}
}

func TestDeployer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deployer Suite")
}

func newApp(labels map[string]string) *unstructured.Unstructured {
	app := newArgoApp()
	app.SetNamespace(argocdNamespace)
	app.SetName("some-serving")
	app.SetLabels(labels)
	return app
}

var _ = Describe("ArgoCDDeployer", func() {
	Describe("EnqueueRequests", func() {
		It("maps an Application event to the Serving's namespace via the serving-namespace label", func() {
			d := NewArgoCD(nil)
			app := newApp(map[string]string{
				deliveryv1alpha1.LabelServing:          "my-serving",
				deliveryv1alpha1.LabelServingNamespace: "my-namespace",
			})

			requests := d.EnqueueRequests(context.Background(), app)

			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Namespace).To(Equal("my-namespace"),
				"the request must target the Serving's namespace, not the argocd namespace")
			Expect(requests[0].Name).To(Equal("my-serving"))
		})

		It("ignores Applications without the serving label", func() {
			d := NewArgoCD(nil)
			app := newApp(map[string]string{
				deliveryv1alpha1.LabelServingNamespace: "my-namespace",
			})

			Expect(d.EnqueueRequests(context.Background(), app)).To(BeNil())
		})

		It("ignores Applications without the serving-namespace label", func() {
			d := NewArgoCD(nil)
			app := newApp(map[string]string{
				deliveryv1alpha1.LabelServing: "my-serving",
			})

			Expect(d.EnqueueRequests(context.Background(), app)).To(BeNil())
		})
	})

	Describe("WatchPredicate", func() {
		It("passes update events where the synced revision changed", func() {
			d := NewArgoCD(nil)
			predicate := d.WatchPredicate()

			oldApp := newApp(nil)
			oldApp.Object["status"] = appStatus("sha256:old")
			newApp := newApp(nil)
			newApp.Object["status"] = appStatus("sha256:new")

			Expect(predicate.Update(event.UpdateEvent{
				ObjectOld: oldApp,
				ObjectNew: newApp,
			})).To(BeTrue(),
				"a revision change must re-trigger reconciliation even when health and sync status are unchanged")
		})

		DescribeTable("passes update events where the opt-in annotation changed",
			func(before, after map[string]string) {
				predicate := NewArgoCD(nil).WatchPredicate()

				oldApp := newApp(nil)
				oldApp.Object["status"] = appStatus("sha256:same")
				oldApp.SetAnnotations(before)
				newApp := newApp(nil)
				newApp.Object["status"] = appStatus("sha256:same")
				newApp.SetAnnotations(after)

				Expect(predicate.Update(event.UpdateEvent{
					ObjectOld: oldApp,
					ObjectNew: newApp,
				})).To(BeTrue(), "a Serving refused for a missing opt-in must resume once the Application opts in")
			},
			Entry("added", nil, map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}),
			Entry("changed",
				map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: "other-order"},
				map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}),
			Entry("removed", map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}, nil),
		)

		It("filters update events where only unrelated annotations changed", func() {
			predicate := NewArgoCD(nil).WatchPredicate()

			oldApp := newApp(nil)
			oldApp.Object["status"] = appStatus("sha256:same")
			newApp := newApp(nil)
			newApp.Object["status"] = appStatus("sha256:same")
			newApp.SetAnnotations(map[string]string{"argocd.argoproj.io/refresh": "normal"})

			Expect(predicate.Update(event.UpdateEvent{
				ObjectOld: oldApp,
				ObjectNew: newApp,
			})).To(BeFalse())
		})

		It("filters update events where nothing relevant changed", func() {
			d := NewArgoCD(nil)
			predicate := d.WatchPredicate()

			oldApp := newApp(nil)
			oldApp.Object["status"] = appStatus("sha256:same")
			newApp := newApp(nil)
			newApp.Object["status"] = appStatus("sha256:same")

			Expect(predicate.Update(event.UpdateEvent{
				ObjectOld: oldApp,
				ObjectNew: newApp,
			})).To(BeFalse())
		})
	})

	Describe("assertAllowedOrderAnnotation", func() {
		It("accepts a matching annotation", func() {
			app := newApp(nil)
			app.SetAnnotations(map[string]string{
				deliveryv1alpha1.AnnotationAllowedOrder: testOrderName,
			})

			Expect(assertAllowedOrderAnnotation(app, testOrderName)).To(Succeed())
		})

		It("rejects a missing annotation with ErrOptInRequired", func() {
			app := newApp(nil)

			err := assertAllowedOrderAnnotation(app, testOrderName)
			Expect(err).To(MatchError(ErrOptInRequired))
		})

		It("rejects a mismatched annotation with ErrOptInRequired", func() {
			app := newApp(nil)
			app.SetAnnotations(map[string]string{
				deliveryv1alpha1.AnnotationAllowedOrder: "other-order",
			})

			err := assertAllowedOrderAnnotation(app, testOrderName)
			Expect(err).To(MatchError(ErrOptInRequired))
		})
	})
})
