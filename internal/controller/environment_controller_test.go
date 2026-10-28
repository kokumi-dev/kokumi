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

package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
)

var _ = Describe("Environment Controller", Ordered, func() {
	const (
		envName = "env-edge"
		project = "env-edge"
		fleet   = "env-fleet"
	)
	ctx := context.Background()
	key := types.NamespacedName{Namespace: testWorkspace, Name: envName}
	edge := map[string]string{fleet: testEdge}
	var r *EnvironmentReconciler

	reconcileEnvironment := func() *deliveryv1alpha1.Environment {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
		return env
	}
	ready := func(conds []metav1.Condition) *metav1.Condition {
		return apimeta.FindStatusCondition(conds, deliveryv1alpha1.ConditionTypeReady)
	}
	createCluster := func(namespace, name string, clusterLabels map[string]string) {
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Cluster{
			Name: name, Namespace: namespace, Labels: clusterLabels,
			Spec: deliveryv1alpha1.ClusterSpec{Connection: deliveryv1alpha1.ClusterConnectionInCluster},
		})).To(Succeed())
	}
	appProject := func(sourceNamespaces ...any) {
		p := &unstructured.Unstructured{}
		p.SetGroupVersionKind(argocd.AppProjectGroupVersionKind)
		p.SetNamespace(argoNamespace)
		p.SetName(project)
		Expect(unstructured.SetNestedSlice(p.Object, []any{map[string]any{keyServer: "*", keyNamespace: "*"}}, "spec", "destinations")).To(Succeed())
		Expect(unstructured.SetNestedSlice(p.Object, sourceNamespaces, "spec", "sourceNamespaces")).To(Succeed())
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
	}

	BeforeAll(func() {
		r = &EnvironmentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Namespace: testKokumiNamespace}
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Environment{
			Name: envName, Namespace: testWorkspace,
			Spec: deliveryv1alpha1.EnvironmentSpec{
				ClusterSelector: metav1.LabelSelector{MatchLabels: edge},
				Deployer:        deliveryv1alpha1.Deployer{Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: project}},
			},
		})).To(Succeed())
	})

	AfterAll(func() {
		env := &deliveryv1alpha1.Environment{}
		if k8sClient.Get(ctx, key, env) == nil {
			env.SetFinalizers(nil)
			_ = k8sClient.Update(ctx, env)
			_ = k8sClient.Delete(ctx, env)
		}
		for _, namespace := range []string{testWorkspace, testKokumiNamespace} {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Cluster{}, client.InNamespace(namespace))
		}
		p := &unstructured.Unstructured{}
		p.SetGroupVersionKind(argocd.AppProjectGroupVersionKind)
		p.SetNamespace(argoNamespace)
		p.SetName(project)
		_ = k8sClient.Delete(ctx, p)
	})

	It("reports that it selects no cluster", func() {
		env := reconcileEnvironment()
		Expect(env.Finalizers).To(ContainElement(deliveryv1alpha1.Finalizer))
		Expect(ready(env.Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonNoClusters))
		Expect(env.Status.Clusters).To(Equal(&deliveryv1alpha1.EnvironmentClusters{}))
	})

	It("reports a missing AppProject, and counts the Clusters of its namespace it selects and those that are ready", func() {
		createCluster(testWorkspace, "env-b", edge)
		createCluster(testWorkspace, "env-a", edge)
		createCluster(testWorkspace, "env-elsewhere", map[string]string{fleet: testRetired})
		createCluster(testKokumiNamespace, "env-other-namespace", edge)
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testWorkspace, Name: "env-a"}, c)).To(Succeed())
		conditions.MarkTrue(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "ready")
		Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())

		env := reconcileEnvironment()
		Expect(ready(env.Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonProjectNotFound))
		Expect(env.Status.Clusters).To(Equal(&deliveryv1alpha1.EnvironmentClusters{Selected: 2, Ready: 1}), "only of its own namespace")
	})

	It("reports an AppProject that does not admit its namespace", func() {
		appProject("team-*")

		Expect(ready(reconcileEnvironment().Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonProjectNotPermitted))
	})

	It("is ready once the AppProject admits its namespace", func() {
		p := &unstructured.Unstructured{}
		p.SetGroupVersionKind(argocd.AppProjectGroupVersionKind)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: argoNamespace, Name: project}, p)).To(Succeed())
		Expect(unstructured.SetNestedSlice(p.Object, []any{testWorkspace}, "spec", "sourceNamespaces")).To(Succeed())
		Expect(k8sClient.Update(ctx, p)).To(Succeed())

		env := reconcileEnvironment()
		Expect(ready(env.Status.Conditions)).To(HaveField("Status", metav1.ConditionTrue))
		Expect(ready(env.Status.Conditions).Message).To(Equal("Deploys to env-a, env-b"))
	})

	It("drops a Cluster that leaves by its labels", func() {
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testWorkspace, Name: "env-b"}, c)).To(Succeed())
		c.Labels[fleet] = testRetired
		Expect(k8sClient.Update(ctx, c)).To(Succeed())

		Expect(reconcileEnvironment().Status.Clusters).To(HaveField("Selected", BeEquivalentTo(1)))
	})

	It("names at most ten Clusters in Ready, and counts the others", func() {
		for i := range 11 {
			createCluster(testWorkspace, fmt.Sprintf("env-many-%02d", i), edge)
		}
		DeferCleanup(func() {
			for i := range 11 {
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Cluster{Namespace: testWorkspace, Name: fmt.Sprintf("env-many-%02d", i)})
			}
		})

		env := reconcileEnvironment()
		Expect(env.Status.Clusters).To(HaveField("Selected", BeEquivalentTo(12)))
		Expect(ready(env.Status.Conditions).Message).To(HaveSuffix(", and 2 more"))
	})

	It("reports a clusterSelector that does not parse", func() {
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
		selector := env.Spec.ClusterSelector
		env.Spec.ClusterSelector = metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: fleet, Operator: metav1.LabelSelectorOpIn},
		}}
		Expect(k8sClient.Update(ctx, env)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Get(ctx, key, env)).To(Succeed())
			env.Spec.ClusterSelector = selector
			Expect(k8sClient.Update(ctx, env)).To(Succeed())
		})

		env = reconcileEnvironment()
		Expect(ready(env.Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonInvalidSelector))
		Expect(env.Status.Clusters).To(BeNil())
	})

	It("in Kokumi's namespace, uses any AppProject", func() {
		inKokumi := types.NamespacedName{Namespace: testKokumiNamespace, Name: envName}
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Environment{
			Name: envName, Namespace: testKokumiNamespace,
			Spec: deliveryv1alpha1.EnvironmentSpec{
				ClusterSelector: metav1.LabelSelector{MatchLabels: edge},
				Deployer:        deliveryv1alpha1.Deployer{Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: project}},
			},
		})).To(Succeed())
		DeferCleanup(func() {
			env := &deliveryv1alpha1.Environment{}
			if k8sClient.Get(ctx, inKokumi, env) == nil {
				env.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, env)
				_ = k8sClient.Delete(ctx, env)
			}
		})

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: inKokumi})
		Expect(err).NotTo(HaveOccurred())
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, inKokumi, env)).To(Succeed())
		Expect(ready(env.Status.Conditions)).To(HaveField("Status", metav1.ConditionTrue), "its sourceNamespaces do not list it")
	})

	It("keeps the Environment local in Kokumi's namespace, and leaves edits to it alone", func() {
		local := types.NamespacedName{Namespace: testKokumiNamespace, Name: cluster.LocalEnvironment}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: local})
		Expect(err).NotTo(HaveOccurred(), "the reconcile at start creates it")
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, local, env)).To(Succeed())
		Expect(env.Spec.ClusterSelector.MatchLabels).To(HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true"))
		Expect(env.Spec.Deployer.ArgoCD.Project).To(Equal(argocd.DefaultProject))

		env.Spec.Description = "Only the platform team"
		Expect(k8sClient.Update(ctx, env)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: local})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, local, env)).To(Succeed())
		Expect(env.Spec.Description).To(Equal("Only the platform team"))

		env.SetFinalizers(nil)
		Expect(k8sClient.Update(ctx, env)).To(Succeed())
		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: local})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, local, env)).To(Succeed(), "a deleted Environment local comes back")
	})

	It("keeps no Environment local in other namespaces", func() {
		_, err := r.Reconcile(ctx, reconcile.Request{Namespace: testWorkspace, Name: cluster.LocalEnvironment})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testWorkspace, Name: cluster.LocalEnvironment},
			&deliveryv1alpha1.Environment{})).NotTo(Succeed())
	})
})

var _ = Describe("Environment mapping", func() {
	It("maps a Cluster to the Environments of its namespace that select it", func() {
		selecting := func(name, namespace, value string) *deliveryv1alpha1.Environment {
			return &deliveryv1alpha1.Environment{Name: name, Namespace: namespace, Spec: deliveryv1alpha1.EnvironmentSpec{
				ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{testFleet: value}},
			}}
		}
		r := &EnvironmentReconciler{Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
			selecting(testEdge, testWorkspace, testEdge), selecting(testRetired, testWorkspace, testRetired),
			selecting("elsewhere", testKokumiNamespace, testEdge),
		).Build()}
		c := &deliveryv1alpha1.Cluster{Name: "eu", Namespace: testWorkspace, Labels: map[string]string{testFleet: testEdge}}

		Expect(r.environmentsOfCluster(context.Background(), c)).To(ConsistOf(
			HaveField("NamespacedName", types.NamespacedName{Namespace: testWorkspace, Name: testEdge})))
	})
})
