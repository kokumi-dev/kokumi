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
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

// The API server acts on behalf of the signed-in user: it impersonates the
// ServiceAccount the user's groups map to, so the UI can do no more than
// kubectl as that ServiceAccount.
var _ = Describe("API server", Label("server"), Serial, func() {
	Context("users who sign in through OIDC as members of kokumi-viewer and kokumi-editor", Ordered, func() {
		var ns string
		var server *framework.Server
		var viewer, editor string

		order := func(name string) map[string]any {
			return map[string]any{
				"namespace": ns, "name": name, "productRef": map[string]string{"name": "podinfo"}, "mode": "Manual",
			}
		}

		BeforeAll(func() {
			By("signing users in through Dex, as make oidc sets up for local development")
			framework.EnableOIDC()
			server = framework.ForwardServer()
			viewer = server.LoginOIDC("viewer@kokumi.dev", "password")
			editor = server.LoginOIDC("editor@kokumi.dev", "password")

			ns = framework.CreateNamespace("server")
			framework.ApplyExample(ns, "getting-started")
		})

		It("lets the viewer read an Order", func() {
			resp, body := server.Do(http.MethodGet, "/api/v1/orders/"+ns+"/podinfo", viewer, nil)
			Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))
		})

		It("refuses the viewer to create an Order, as RBAC refuses the kokumi-viewer ServiceAccount", func() {
			resp, body := server.Do(http.MethodPost, "/api/v1/orders", viewer, order("from-viewer"))
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden), string(body))
		})

		It("lets the editor create an Order, which Kokumi renders", func() {
			resp, body := server.Do(http.MethodPost, "/api/v1/orders", editor, order("from-editor"))
			Expect(resp.StatusCode).To(Equal(http.StatusCreated), string(body))
			framework.LatestPreparation(ns, "from-editor")
		})
	})
})
