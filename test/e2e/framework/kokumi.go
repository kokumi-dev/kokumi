/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// LatestPreparation waits until the Order order in namespace has rendered a
// Preparation other than the ones in previous, and the Preparation is
// Ready, and returns it.
func LatestPreparation(namespace, order string, previous ...string) *deliveryv1alpha1.Preparation {
	var preparation *deliveryv1alpha1.Preparation
	Eventually(func(g Gomega) {
		o, err := Order(namespace, order)()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(o.Status.LatestPreparationName).NotTo(BeEmpty(), "The Order has rendered no Preparation")
		g.Expect(previous).NotTo(ContainElement(o.Status.LatestPreparationName), "The Order has rendered no new Preparation")
		p, err := Preparation(namespace, o.Status.LatestPreparationName)()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p).To(BeReady())
		preparation = p
	}, RenderTimeout).Should(Succeed(), "The Order %s has no new Ready Preparation", order)
	return preparation
}

// Promote promotes the Preparation preparation of the Order order in
// namespace, as a user does with kubectl: it sets the preparationName of the
// Order's Serving, and creates the Serving when the Order has none yet.
func Promote(namespace, order, preparation string) {
	serving := &deliveryv1alpha1.Serving{}
	serving.Namespace, serving.Name = namespace, order
	Eventually(func() error {
		_, err := controllerutil.CreateOrUpdate(context.Background(), Client, serving, func() error {
			if serving.Labels == nil {
				serving.Labels = map[string]string{}
			}
			serving.Labels[deliveryv1alpha1.LabelOrder] = order
			serving.Spec.OrderName = order
			serving.Spec.PreparationName = preparation
			return nil
		})
		return err
	}).Should(Succeed(), "Failed to promote %s", preparation)
}

// Requeue annotates the object of resource name in namespace, so that its
// controller reconciles it again without waiting for its retry interval.
func Requeue(resource, namespace, name string) {
	_, err := Kubectl("annotate", resource, name, "-n", namespace,
		"e2e.kokumi.dev/requeue="+time.Now().Format(time.RFC3339Nano), "--overwrite")
	Expect(err).NotTo(HaveOccurred())
}

// PatchKitchen merges the patch in file, relative to the repository, into the
// Kitchen as a cluster admin would with
// kubectl patch kitchen default -n kokumi --type=merge --patch-file <file>,
// and restores the Kitchen's spec when the scenario ends.
func PatchKitchen(file string) {
	restoreKitchenAfterScenario()
	_, err := Kubectl("patch", "kitchen", "default", "-n", InstallNamespace, "--type=merge",
		"--patch-file", filepath.Join(ProjectDir(), file))
	Expect(err).NotTo(HaveOccurred(), "Failed to patch the Kitchen with %s", file)
}

// restoreKitchenAfterScenario restores the Kitchen's current spec when the
// scenario ends.
func restoreKitchenAfterScenario() {
	kitchen := &deliveryv1alpha1.Kitchen{}
	key := types.NamespacedName{Namespace: InstallNamespace, Name: "default"}
	Expect(Client.Get(context.Background(), key, kitchen)).To(Succeed())
	saved := kitchen.Spec.DeepCopy()
	DeferCleanup(func() {
		Update(InstallNamespace, "default", func(k *deliveryv1alpha1.Kitchen) { k.Spec = *saved })
	})
}

// Values returns Helm values for an Order or Product from JSON.
func Values(json string) *apiextensionsv1.JSON {
	return &apiextensionsv1.JSON{Raw: []byte(json)}
}
