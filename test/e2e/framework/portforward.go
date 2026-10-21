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
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// forward forwards a free local port to each of ports of a ready Pod behind
// the Service service in namespace until the scope of the calling node ends,
// as kubectl port-forward does, and returns the local host:port of each, in
// the order of ports. ports are the Pod's container ports.
func forward(namespace, service string, ports ...int) []string {
	pod := readyPod(namespace, service)

	clientset, err := kubernetes.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())
	url := clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("portforward").URL()

	// WebSockets first, SPDY for API servers without them, as kubectl does.
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	Expect(err).NotTo(HaveOccurred())
	spdyDialer := spdy.NewDialerForStreaming(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	websocketDialer, err := portforward.NewSPDYOverWebsocketDialerForStreaming(url, restConfig)
	Expect(err).NotTo(HaveOccurred())
	dialer := portforward.NewFallbackDialerForStreaming(websocketDialer, spdyDialer, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})

	specs := make([]string, len(ports))
	for i, port := range ports {
		specs[i] = fmt.Sprintf(":%d", port) // a free local port
	}
	ctx, stop := context.WithCancel(context.Background())
	DeferCleanup(stop)
	ready := make(chan struct{})
	forwarder, err := portforward.NewOnAddressesForStreamingWithContext(ctx, dialer, []string{"127.0.0.1"}, specs,
		ready, GinkgoWriter, GinkgoWriter)
	Expect(err).NotTo(HaveOccurred())
	failed := make(chan error, 1)
	go func() { failed <- forwarder.ForwardPorts() }()

	select {
	case <-ready:
	case err := <-failed:
		Fail(fmt.Sprintf("Failed to port-forward to %s/%s: %v", namespace, pod, err))
	case <-time.After(time.Minute):
		Fail(fmt.Sprintf("Timed out port-forwarding to %s/%s", namespace, pod))
	}

	forwarded, err := forwarder.GetPorts()
	Expect(err).NotTo(HaveOccurred())
	local := map[int]uint16{}
	for _, f := range forwarded {
		local[int(f.Remote)] = f.Local
	}
	hosts := make([]string, len(ports))
	for i, port := range ports {
		hosts[i] = fmt.Sprintf("127.0.0.1:%d", local[port])
	}
	return hosts
}

// readyPod waits for a ready Pod, not being deleted, behind the Service
// service in namespace and returns its name.
func readyPod(namespace, service string) string {
	ctx := context.Background()
	svc := &corev1.Service{}
	Expect(Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: service}, svc)).To(Succeed())

	var name string
	Eventually(func(g Gomega) {
		var pods corev1.PodList
		g.Expect(Client.List(ctx, &pods, client.InNamespace(namespace),
			client.MatchingLabels(svc.Spec.Selector))).To(Succeed())
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp == nil && podReady(pod) {
				name = pod.Name
				return
			}
		}
		g.Expect(name).NotTo(BeEmpty(), "No Pod behind the Service %s/%s is ready", namespace, service)
	}, 2*time.Minute).Should(Succeed())
	return name
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
