//go:build e2e

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

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Clusters", Label("clusters"), func() {
	Context("the cluster Kokumi runs in", func() {
		It("is kept as the Cluster in-cluster in Kokumi's namespace, ready, with its server and version", func() {
			inCluster := framework.Cluster(framework.InstallNamespace, cluster.InCluster)
			Eventually(inCluster, framework.ClusterTimeout).Should(SatisfyAll(
				HaveField("Labels", HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true")),
				framework.HaveReady(metav1.ConditionTrue, deliveryv1alpha1.ReasonReady),
				HaveField("Status.Server", cluster.InClusterServer),
				HaveField("Status.KubernetesVersion", HavePrefix("v")),
			))
		})
	})
})
