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
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"regexp"
	"time"

	. "github.com/onsi/gomega" //nolint:revive,staticcheck

	"github.com/kokumi-dev/kokumi/test/utils"
)

// Server is the kokumi API server as a browser reaches it, through a
// port-forward to the kokumi-server Pod. With the Dex sidecar of hack/oidc
// ("make oidc") the Pod also serves Dex.
type Server struct {
	transport *http.Transport
}

// The OIDC flow redirects between these addresses, which Dex's client
// configuration in hack/oidc names, so a browser must reach them as such.
const (
	serverAddress = "localhost:8080"
	dexAddress    = "localhost:5556"
)

// EnableOIDC adds Dex to the kokumi-server Pod and has the Kitchen sign users
// in through it, as "make oidc" does for local development (see hack/oidc),
// and restores the Kitchen's spec when the scenario ends. Dex's password
// database holds admin, editor, viewer, approver1 and approver2, all
// @kokumi.dev with the password "password"; the approvers are members of
// release-approvers.
func EnableOIDC() {
	restoreKitchenAfterScenario()
	_, err := utils.Run(exec.Command("make", "oidc"))
	Expect(err).NotTo(HaveOccurred(), "Failed to enable OIDC through Dex")
}

// ForwardServer forwards free local ports to the API server and to Dex in
// the kokumi-server Pod until the scope of the calling node ends.
func ForwardServer() *Server {
	hosts := forward(InstallNamespace, "kokumi-server", 8080, 5556)
	forwarded := map[string]string{serverAddress: hosts[0], dexAddress: hosts[1]}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	s := &Server{transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if local, ok := forwarded[address]; ok {
				address = local
			}
			return dialer.DialContext(ctx, network, address)
		},
	}}

	Eventually(func(g Gomega) {
		resp, err := s.client().Get("http://" + serverAddress + "/healthz")
		g.Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close() //nolint:errcheck
		g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
	}, time.Minute).Should(Succeed(), "The API server is not reachable through the port-forward")
	return s
}

// Login signs in with a username and password, as the built-in admin user
// does, and returns the access token.
func (s *Server) Login(username, password string) string {
	credentials := map[string]string{"username": username, "password": password}
	resp, body := s.Do(http.MethodPost, "/api/v1/auth/login", "", credentials)
	Expect(resp.StatusCode).To(Equal(http.StatusOK), "Failed to log in as %s: %s", username, body)
	var session struct {
		Token string `json:"token"`
	}
	Expect(json.Unmarshal(body, &session)).To(Succeed())
	return session.Token
}

var formAction = regexp.MustCompile(`<form[^>]*action="([^"]*)"`)

// LoginOIDC signs in through OIDC as a user of Dex's password database, as a
// browser would: it starts the flow at the API server, submits the user's
// credentials to Dex's login form and follows the redirects back to the API
// server, which hands the access token to the UI in the URL fragment.
func (s *Server) LoginOIDC(email, password string) string {
	jar, err := cookiejar.New(nil)
	Expect(err).NotTo(HaveOccurred())
	var token string
	// The API server hands the token over in the fragment of its last
	// redirect, which a browser would follow to the UI.
	stopAtToken := func(req *http.Request, _ []*http.Request) error {
		if fragment, err := url.ParseQuery(req.URL.Fragment); err == nil && fragment.Get("access_token") != "" {
			token = fragment.Get("access_token")
			return http.ErrUseLastResponse
		}
		return nil
	}
	browser := &http.Client{Jar: jar, Transport: s.transport, CheckRedirect: stopAtToken}

	resp, err := browser.Get("http://" + serverAddress + "/api/v1/auth/oidc/start")
	Expect(err).NotTo(HaveOccurred())
	page, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK), "Dex shows no login form: %s", page)

	login := resp.Request.URL
	if match := formAction.FindSubmatch(page); match != nil && len(match[1]) > 0 {
		login, err = login.Parse(html.UnescapeString(string(match[1])))
		Expect(err).NotTo(HaveOccurred())
	}
	resp, err = browser.PostForm(login.String(), url.Values{"login": {email}, "password": {password}})
	Expect(err).NotTo(HaveOccurred())
	_ = resp.Body.Close()
	Expect(token).NotTo(BeEmpty(), "The OIDC login of %s ended without an access token, at %s", email, resp.Request.URL)
	return token
}

// Do sends a request with a JSON body, unless body is nil, to the API server,
// authenticated with token unless it is empty, and returns the response and
// its body.
func (s *Server) Do(method, path, token string, body any) (*http.Response, []byte) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+serverAddress+path, reader)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close() //nolint:errcheck
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp, data
}

func (s *Server) client() *http.Client {
	return &http.Client{Transport: s.transport, Timeout: 30 * time.Second}
}
