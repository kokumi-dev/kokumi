package registry

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Reader reads the Registries objects list and the Secrets those name. Its
// errors for an object that does not exist satisfy apierrors.IsNotFound.
type Reader interface {
	Registry(ctx context.Context, namespace, name string) (*deliveryv1alpha1.Registry, error)
	Secret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
}

// Resolver connects through the Registries objects list. It reads them and
// their Secrets with its Reader and connects with the clients Clients keeps.
type Resolver struct {
	clients *Clients
	reader  Reader
}

// NewResolver returns a Resolver that reads with reader and connects with
// clients.
func NewResolver(clients *Clients, reader Reader) *Resolver {
	return &Resolver{clients: clients, reader: reader}
}

// Scope reads the Registries refs name in namespace, with their Secrets, and
// returns the Scope they form. A Registry that does not exist fails the
// read: whoever listed it meant something that anonymous access would hide.
// A Secret that cannot be read only fails connections through its Registry.
func (r *Resolver) Scope(ctx context.Context, namespace string, refs []corev1.LocalObjectReference) (Scope, error) {
	entries := make([]Entry, 0, len(refs))
	for _, ref := range refs {
		reg, err := r.reader.Registry(ctx, namespace, ref.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return Scope{}, RegistryNotFound(ref.Name)
			}
			return Scope{}, fmt.Errorf("failed to get Registry %q: %w", ref.Name, err)
		}
		entries = append(entries, r.entry(ctx, reg))
	}
	return r.clients.Scope(entries), nil
}

// Connect reads the Secrets reg names and returns the connection through
// reg to its url.
func (r *Resolver) Connect(ctx context.Context, reg *deliveryv1alpha1.Registry) (Connection, error) {
	return r.clients.Connect(r.entry(ctx, reg))
}

// ConnectRecorded returns the connection to url, an artifact Kokumi pushed:
// through the recorded Registry that authenticated the push while it exists
// and still covers url, and through the Registries fallback names otherwise,
// for example when the Registry was deleted or the artifact predates it.
// fallback is only called when the recorded Registry does not connect.
func (r *Resolver) ConnectRecorded(
	ctx context.Context,
	namespace string,
	recorded *corev1.LocalObjectReference,
	url string,
	fallback func(context.Context) ([]corev1.LocalObjectReference, error),
) (Connection, error) {
	if recorded != nil {
		scope, err := r.Scope(ctx, namespace, []corev1.LocalObjectReference{*recorded})
		if err == nil {
			conn, err := scope.Connect(url)
			if err != nil || conn.Registry != nil {
				return conn, err
			}
		} else if Reason(err, "") != deliveryv1alpha1.ReasonRegistryNotFound {
			return Connection{}, err
		}
	}

	refs, err := fallback(ctx)
	if err != nil {
		return Connection{}, err
	}
	scope, err := r.Scope(ctx, namespace, refs)
	if err != nil {
		return Connection{}, err
	}
	return scope.Connect(url)
}

// entry reads the Secrets reg names.
func (r *Resolver) entry(ctx context.Context, reg *deliveryv1alpha1.Registry) Entry {
	entry := Entry{Registry: reg}
	for _, s := range []struct {
		ref    *corev1.LocalObjectReference
		secret **corev1.Secret
	}{
		{reg.Spec.SecretRef, &entry.Secret},
		{reg.Spec.CertSecretRef, &entry.CertSecret},
	} {
		if s.ref == nil {
			continue
		}
		secret, err := r.reader.Secret(ctx, reg.Namespace, s.ref.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				entry.Err = SecretNotFound(s.ref.Name)
			} else {
				entry.Err = fmt.Errorf("read Secret %q: %w", s.ref.Name, err)
			}
			return entry
		}
		*s.secret = secret
	}
	return entry
}
