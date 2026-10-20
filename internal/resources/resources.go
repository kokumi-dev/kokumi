// Package resources builds the objects the Kokumi controllers create: the
// Preparation and Serving of an Order, the Secrets of the Kitchen, and the
// default Kitchen and Registry. It only builds them; the controllers set
// owner references and write them.
package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// Keys of the token signing-key Secret.
const (
	// TokenSigningKey holds the HMAC key the server signs session tokens
	// with.
	TokenSigningKey = "signing-key"
)

// Rendered is an artifact an Order rendered, as its Preparation records it.
type Rendered struct {
	// Source is the source the artifact was rendered from, with its tag and
	// digest.
	Source oci.Reference
	// Artifact is the pushed artifact, with its digest.
	Artifact oci.Reference
	// RegistryName names the Registry that authenticated the push, "" when
	// none did.
	RegistryName string
	Git          deliveryv1alpha1.GitSource
	Images       []images.Image
	Renderer     deliveryv1alpha1.Renderer
	// ConfigHash is the hash of the intent the artifact renders.
	ConfigHash string
	// ProductRevision is the Product revision the artifact renders.
	ProductRevision string
	CommitMessage   string
	// ParentDigest is the digest of the artifact the Order rendered before,
	// "" for its first.
	ParentDigest string
}

// Preparation returns the Preparation that records rendered for order. It is
// named after the Order and the artifact digest, so a render of the same
// artifact finds it again.
func Preparation(order *deliveryv1alpha1.Order, rendered Rendered) *deliveryv1alpha1.Preparation {
	preparation := &deliveryv1alpha1.Preparation{
		Name:      fmt.Sprintf("%s-%s", order.Name, rendered.Artifact.ShortDigest()),
		Namespace: order.Namespace,
		Labels: map[string]string{
			deliveryv1alpha1.LabelOrder:   order.Name,
			deliveryv1alpha1.LabelVersion: rendered.Source.Tag,
		},
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: order.Name,
			Source: deliveryv1alpha1.OrderSource{
				URL:        rendered.Source.OCIRepositoryReference(),
				BaseDigest: rendered.Source.Digest,
			},
			Renderer:        rendered.Renderer,
			ConfigHash:      rendered.ConfigHash,
			ProductRevision: rendered.ProductRevision,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: rendered.Artifact.OCIString(),
				Digest: rendered.Artifact.Digest,
			},
			CommitMessage: rendered.CommitMessage,
			ParentDigest:  rendered.ParentDigest,
			GitSource:     rendered.Git,
			Promotion:     order.PromotionGates(),
			Images:        preparationImages(rendered.Images),
		},
	}
	if rendered.RegistryName != "" {
		preparation.Spec.Artifact.RegistryRef = &corev1.LocalObjectReference{Name: rendered.RegistryName}
	}
	return preparation
}

// preparationImages converts the images of a render into their Preparation
// form, which leaves out why an image could not be resolved.
func preparationImages(imgs []images.Image) []deliveryv1alpha1.PreparationImage {
	if len(imgs) == 0 {
		return nil
	}

	out := make([]deliveryv1alpha1.PreparationImage, 0, len(imgs))
	for _, img := range imgs {
		out = append(out, deliveryv1alpha1.PreparationImage{
			Ref:       img.Ref,
			Name:      img.Name,
			Digest:    img.Digest,
			MediaType: img.MediaType,
			Vendored:  img.Vendored,
		})
	}
	return out
}

// Serving returns the Serving of order, which carries the Order's name.
func Serving(order *deliveryv1alpha1.Order) *deliveryv1alpha1.Serving {
	return &deliveryv1alpha1.Serving{
		Name:      order.Name,
		Namespace: order.Namespace,
		Labels:    map[string]string{deliveryv1alpha1.LabelOrder: order.Name},
		Spec:      deliveryv1alpha1.ServingSpec{OrderName: order.Name},
	}
}

// Kitchen returns the singleton Kitchen of the install namespace, with
// every setting at its default.
func Kitchen(namespace string) *deliveryv1alpha1.Kitchen {
	return &deliveryv1alpha1.Kitchen{
		Name:      deliveryv1alpha1.DefaultKitchenName,
		Namespace: namespace,
	}
}

// BundledRegistry returns the Registry of the bundled registry at host, in
// the install namespace. It is reached over HTTP.
func BundledRegistry(namespace, host string) *deliveryv1alpha1.Registry {
	return &deliveryv1alpha1.Registry{
		Name:      deliveryv1alpha1.DefaultRegistryName,
		Namespace: namespace,
		Labels:    map[string]string{"app.kubernetes.io/managed-by": "kokumi"},
		Spec: deliveryv1alpha1.RegistrySpec{
			URL:    "oci://" + host,
			Scheme: deliveryv1alpha1.RegistrySchemeHTTP,
		},
	}
}

// SigningKeySecret returns the signing key Secret in namespace, holding key
// in cosign's layout.
func SigningKeySecret(namespace string, key signing.GeneratedKey) *corev1.Secret {
	return &corev1.Secret{
		Name:      deliveryv1alpha1.SigningKeySecretName,
		Namespace: namespace,
		Labels:    kitchenSecretLabels("controller"),
		Data: map[string][]byte{
			signing.SecretPrivateKey: key.PrivateKey,
			signing.SecretPassword:   key.Password,
			signing.SecretPublicKey:  key.PublicKey,
		},
	}
}

// TokenSigningKeySecret returns the Secret name in namespace that holds the
// key the server signs session tokens with.
func TokenSigningKeySecret(namespace, name string, key []byte) *corev1.Secret {
	return &corev1.Secret{
		Name:      name,
		Namespace: namespace,
		Labels:    kitchenSecretLabels("server"),
		Data:      map[string][]byte{TokenSigningKey: key},
	}
}

// kitchenSecretLabels returns the labels of a Secret the Kitchen controller
// generates for component.
func kitchenSecretLabels(component string) map[string]string {
	const app = "kokumi"
	return map[string]string{
		"app.kubernetes.io/name":       app,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": app,
	}
}
