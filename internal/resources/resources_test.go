package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

const (
	testNamespace = "team-a"
	testOrder     = "app"
	testDigest    = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func testOrderObject(policy *deliveryv1alpha1.ApprovalPolicy) *deliveryv1alpha1.Order {
	return &deliveryv1alpha1.Order{
		Name:      testOrder,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.OrderSpec{
			Promotion: &deliveryv1alpha1.PromotionSpec{Approvals: policy},
		},
	}
}

func mustParse(t *testing.T, ref string) oci.Reference {
	t.Helper()
	parsed, err := oci.Parse(ref)
	require.NoError(t, err)
	return parsed
}

func TestPreparation(t *testing.T) {
	policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1}
	order := testOrderObject(policy)

	prep := Preparation(order, Rendered{
		Source:       mustParse(t, "oci://ghcr.io/org/chart:1.0.0@"+testDigest),
		Artifact:     mustParse(t, "oci://registry.example/order/app:1.0.0@"+testDigest),
		RegistryName: "rendered",
		Git:          deliveryv1alpha1.GitSource{Repo: "https://github.com/org/app", CommitHash: "abc"},
		Images: []images.Image{
			{Ref: "nginx:1.27", Name: "docker.io/library/nginx", Digest: testDigest},
			{Ref: "unknown:latest", Error: "not found"},
		},
		Renderer:      deliveryv1alpha1.Renderer{Type: deliveryv1alpha1.RenderTypeManifest},
		ConfigHash:    "sha256:intent",
		CommitMessage: "Initial commit",
	})

	assert.Equal(t, "app-0123456789ab", prep.Name, "named after the Order and the artifact digest")
	assert.Equal(t, testNamespace, prep.Namespace)
	assert.Equal(t, map[string]string{deliveryv1alpha1.LabelOrder: testOrder, deliveryv1alpha1.LabelVersion: "1.0.0"}, prep.Labels)
	assert.Equal(t, "oci://ghcr.io/org/chart", prep.Spec.Source.URL)
	assert.Equal(t, testDigest, prep.Spec.Source.BaseDigest)
	assert.Equal(t, testDigest, prep.Spec.Artifact.Digest)
	assert.Equal(t, &corev1.LocalObjectReference{Name: "rendered"}, prep.Spec.Artifact.RegistryRef)
	assert.Equal(t, "sha256:intent", prep.Spec.ConfigHash)
	assert.Equal(t, "Initial commit", prep.Spec.CommitMessage)
	assert.Equal(t, []deliveryv1alpha1.PreparationImage{
		{Ref: "nginx:1.27", Name: "docker.io/library/nginx", Digest: testDigest},
		{Ref: "unknown:latest"},
	}, prep.Spec.Images, "the Preparation leaves out why an image could not be resolved")

	require.Equal(t, policy, prep.Spec.ApprovalPolicy)
	assert.NotSame(t, policy, prep.Spec.ApprovalPolicy, "the policy is a snapshot")
	assert.Nil(t, prep.Spec.Checks)

	anonymous := Preparation(testOrderObject(nil), Rendered{
		Source:   mustParse(t, "oci://ghcr.io/org/chart:1.0.0@"+testDigest),
		Artifact: mustParse(t, "oci://registry.example/order/app:1.0.0@"+testDigest),
	})
	assert.Nil(t, anonymous.Spec.Artifact.RegistryRef, "no Registry authenticated the push")
	assert.Nil(t, anonymous.Spec.ApprovalPolicy)
	assert.Nil(t, anonymous.Spec.Images)
}

func TestPreparation_checks(t *testing.T) {
	checks := &deliveryv1alpha1.PromotionChecks{
		Vulnerabilities: &deliveryv1alpha1.VulnerabilityCheck{MaxSeverity: deliveryv1alpha1.SeverityHigh},
	}
	order := testOrderObject(nil)
	order.Spec.Promotion.Checks = checks

	prep := Preparation(order, Rendered{
		Source:   mustParse(t, "oci://ghcr.io/org/chart:1.0.0@"+testDigest),
		Artifact: mustParse(t, "oci://registry.example/order/app:1.0.0@"+testDigest),
	})

	require.Equal(t, checks, prep.Spec.Checks)
	assert.NotSame(t, checks, prep.Spec.Checks, "the checks are a snapshot")
	assert.NotSame(t, checks.Vulnerabilities, prep.Spec.Checks.Vulnerabilities)
}

func TestServing(t *testing.T) {
	serving := Serving(testOrderObject(nil))
	assert.Equal(t, testOrder, serving.Name, "a Serving carries the name of its Order")
	assert.Equal(t, testNamespace, serving.Namespace)
	assert.Equal(t, testOrder, serving.Spec.OrderName)
	assert.Equal(t, testOrder, serving.Labels[deliveryv1alpha1.LabelOrder])
	assert.Empty(t, serving.Spec.PreparationName)
}

func TestBundledRegistry(t *testing.T) {
	reg := BundledRegistry("kokumi", "kokumi-registry.kokumi.svc:5000")
	assert.Equal(t, deliveryv1alpha1.DefaultRegistryName, reg.Name)
	assert.Equal(t, "oci://kokumi-registry.kokumi.svc:5000", reg.Spec.URL)
	assert.Equal(t, deliveryv1alpha1.RegistrySchemeHTTP, reg.Spec.Scheme)
}

func TestKitchenSecrets(t *testing.T) {
	key := signing.GeneratedKey{PrivateKey: []byte("private"), Password: []byte("password"), PublicKey: []byte("public")}
	signingKey := SigningKeySecret("kokumi-signing", key)
	assert.Equal(t, deliveryv1alpha1.SigningKeySecretName, signingKey.Name)
	assert.Equal(t, map[string][]byte{
		signing.SecretPrivateKey: []byte("private"),
		signing.SecretPassword:   []byte("password"),
		signing.SecretPublicKey:  []byte("public"),
	}, signingKey.Data)
	assert.Equal(t, "controller", signingKey.Labels["app.kubernetes.io/component"])

	token := TokenSigningKeySecret("kokumi", "token", []byte("key"))
	assert.Equal(t, map[string][]byte{TokenSigningKey: []byte("key")}, token.Data)
	assert.Equal(t, "server", token.Labels["app.kubernetes.io/component"])
}
