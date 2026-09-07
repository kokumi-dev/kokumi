package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// userClient resolves, for the authenticated user of a request, the Kubernetes
// client(s) to execute operations with. It enforces Kubernetes RBAC by
// impersonating the mapped ServiceAccounts; the server's own permissions are
// never used for user-facing operations.
type userClient struct {
	imp *impersonator
	sas []corev1.ServiceAccount // mapped SAs, sorted by name
}

// errNoMapping is returned when an authenticated identity maps to no
// ServiceAccount (or maps to SAs that lack the required permission).
var errNoMapping = fmt.Errorf("no ServiceAccount is mapped to this user")

// isDenied reports whether the error is an authorization denial (RBAC 403 from
// the API server, no mapped SA, or no permitted SA for the operation).
func isDenied(err error) bool {
	return apierrors.IsForbidden(err) || errors.Is(err, errNoMapping) || errors.Is(err, errNotAuthorized)

}

// resolveUserClient maps the request's identity to ServiceAccounts and returns
// a userClient. Returns an error suitable for a 403 when no SA matches.
func (deps *apiDeps) resolveUserClient(r *http.Request) (*userClient, error) {
	id := identityFromRequest(r)
	if id == nil {
		// Auth disabled: no identity, no impersonation. This should not
		// happen for protected paths; treat as unauthorized.
		return nil, errNoMapping
	}
	sas := resolveServiceAccounts(deps.saList(), id)
	if len(sas) == 0 {
		return nil, errNoMapping
	}
	return &userClient{imp: deps.impersonator, sas: sas}, nil
}

// get performs a Get as the first mapped ServiceAccount that can read the
// object: each SA is tried in order and the first success is returned. A
// NotFound is remembered and returned only if no SA can read the object, so a
// later SA that can still wins.
func (u *userClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	var notFound error
	for _, sa := range u.sas {
		c, err := u.imp.clientFor(sa.Name)
		if err != nil {
			return err
		}
		err = c.Get(ctx, key, obj)
		if err == nil {
			return nil
		}
		if apierrors.IsNotFound(err) {
			notFound = err
		}
	}
	if notFound != nil {
		return notFound
	}
	return errNoMapping
}

// list performs a List as each mapped ServiceAccount and merges the results:
// items visible to any mapped SA are returned (union). Duplicates (an object
// visible to more than one SA) are removed by namespace/name. Failures of
// individual SAs (e.g. RBAC 403 on part of the union) do not fail the call.
func (u *userClient) list(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	seen := map[types.NamespacedName]struct{}{}
	var merged []runtime.Object
	for _, sa := range u.sas {
		c, err := u.imp.clientFor(sa.Name)
		if err != nil {
			return err
		}
		perSA := list.DeepCopyObject().(client.ObjectList)
		if err := c.List(ctx, perSA, opts...); err != nil {
			continue // this SA cannot list here; the union just misses its part
		}
		saItems, err := meta.ExtractList(perSA)
		if err != nil {
			continue
		}
		for _, it := range saItems {
			obj, ok := it.(client.Object)
			if !ok {
				continue
			}
			key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, it)
		}
	}
	if err := meta.SetList(list, merged); err != nil {
		return fmt.Errorf("merging lists: %w", err)
	}
	if len(u.sas) == 0 {
		return errNoMapping
	}
	return nil
}

// writeSA picks the mapped SA authorized for the verb on the resource in the
// namespace and returns its client.
func (u *userClient) writeSA(ctx context.Context, verb, resource, namespace string) (client.Client, error) {
	name, err := u.imp.pickWriteSA(ctx, u.sas, verb, resource, namespace)
	if err != nil {
		if errors.Is(err, errNotAuthorized) {
			return nil, fmt.Errorf("%w to %s %s", errNotAuthorized, verb, resource)
		}
		return nil, err
	}
	return u.imp.clientFor(name)
}

// create executes a Create as the first mapped SA authorized to create the
// resource in the object's namespace.
func (u *userClient) create(ctx context.Context, obj client.Object, resource string) error {
	ns := obj.GetNamespace()
	c, err := u.writeSA(ctx, "create", resource, ns)
	if err != nil {
		return err
	}
	return c.Create(ctx, obj)
}

// update executes an Update as the first mapped SA authorized to update the
// resource in the object's namespace.
func (u *userClient) update(ctx context.Context, obj client.Object, resource string) error {
	ns := obj.GetNamespace()
	c, err := u.writeSA(ctx, "update", resource, ns)
	if err != nil {
		return err
	}
	return c.Update(ctx, obj)
}

// patch executes a Patch as the first mapped SA authorized to patch the
// resource in the object's namespace.
func (u *userClient) patch(ctx context.Context, obj client.Object, patch client.Patch, resource string) error {
	ns := obj.GetNamespace()
	c, err := u.writeSA(ctx, "patch", resource, ns)
	if err != nil {
		return err
	}
	return c.Patch(ctx, obj, patch)
}

// delete executes a Delete as the first mapped SA authorized to delete the
// resource in the object's namespace.
func (u *userClient) delete(ctx context.Context, obj client.Object, resource string) error {
	ns := obj.GetNamespace()
	c, err := u.writeSA(ctx, "delete", resource, ns)
	if err != nil {
		return err
	}
	return c.Delete(ctx, obj)
}

// readerFor returns a client.Reader restricted to the first mapped SA that can
// read the resource in the namespace (used for helper paths like credential
// resolution). Returns errNotAuthorized when no mapped SA is permitted.
func (u *userClient) readerFor(ctx context.Context, verb, resource, namespace string) (client.Reader, error) {
	for _, sa := range u.sas {
		allowed, err := u.imp.authorizedFor(ctx, sa.Name, verb, resource, namespace)
		if err != nil {
			return nil, err
		}
		if allowed {
			return u.imp.clientFor(sa.Name)
		}
	}
	return nil, fmt.Errorf("%w to %s %s", errNotAuthorized, verb, resource)
}

// respondForbiddenOrError maps operation errors to HTTP responses: RBAC
// denials and no-mapping errors become 403; other errors use the provided
// fallback status.
func respondForbiddenOrError(w http.ResponseWriter, err error, op string) {
	if isDenied(err) {
		respondError(w, http.StatusForbidden, err.Error())
		return
	}
	respondError(w, http.StatusInternalServerError, fmt.Sprintf("%s: %s", op, err))
}
