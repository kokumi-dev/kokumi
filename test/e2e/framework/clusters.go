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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ClusterTimeout covers what Kokumi checks of a cluster once a minute, such
// as whether it accepts its credential.
const ClusterTimeout = 3 * time.Minute

// GrantClusterCredentials binds the ClusterRole kokumi-cluster-credentials
// in namespace to the admin identity, as the operator guide tells operators
// to for each workspace whose Clusters the admin registers with a
// kubeconfig. It goes with the namespace.
func GrantClusterCredentials(namespace string) {
	By("binding kokumi-cluster-credentials to the admin identity in namespace " + namespace)
	_, err := Kubectl("create", "rolebinding", "kokumi-cluster-credentials", "-n", namespace,
		"--clusterrole=kokumi-cluster-credentials", "--serviceaccount="+InstallNamespace+":kokumi-admin")
	Expect(err).NotTo(HaveOccurred(), "Failed to bind kokumi-cluster-credentials in %s", namespace)
}

// Cluster returns a function for Eventually that fetches the Cluster name
// of namespace.
func Cluster(namespace, name string) func() (*deliveryv1alpha1.Cluster, error) {
	return Object[deliveryv1alpha1.Cluster](namespace, name)
}

// HaveReady succeeds for an object, such as a Cluster, whose Ready
// condition has status and reason.
func HaveReady(status metav1.ConditionStatus, reason string) OmegaMatcher {
	return SatisfyAll(HaveCondition(deliveryv1alpha1.ConditionTypeReady, status),
		HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, reason))
}
