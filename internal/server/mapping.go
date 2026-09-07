package server

import (
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Identity-mapping annotations on ServiceAccounts in the install namespace.
// An identity matches a ServiceAccount when any of these hold:
//   - identity-sub equals the token's subject claim
//   - identity-email equals the token's email claim
//   - any of the token's groups appears in the comma-separated identity-groups
//
// Multiple annotations on one ServiceAccount are OR-ed. A user may match
// multiple ServiceAccounts; effective permissions are the union.
const (
	annotationIdentitySub    = "kokumi.dev/identity-sub"
	annotationIdentityEmail  = "kokumi.dev/identity-email"
	annotationIdentityGroups = "kokumi.dev/identity-groups"
)

// adminServiceAccountName is the ServiceAccount the built-in admin login acts
// as. It is not matched via annotations (the admin identity is recognized by
// provider=admin in the session) so OIDC users cannot accidentally match it.
const adminServiceAccountName = "kokumi-admin"

// matches reports whether the identity matches the ServiceAccount's mapping
// annotations. SAs without any identity annotation never match.
func matchesIdentity(sa *corev1.ServiceAccount, id *Identity) bool {
	if id == nil {
		return false
	}
	ann := sa.Annotations
	if len(ann) == 0 {
		return false
	}
	if sub, ok := ann[annotationIdentitySub]; ok && sub != "" && sub == id.Subject {
		return true
	}
	if email, ok := ann[annotationIdentityEmail]; ok && email != "" && id.Email != "" && email == id.Email {
		return true
	}
	if groups, ok := ann[annotationIdentityGroups]; ok && groups != "" && len(id.Groups) > 0 {
		for g := range strings.SplitSeq(groups, ",") {
			g = strings.TrimSpace(g)
			if g != "" && slices.Contains(id.Groups, g) {
				return true
			}
		}
	}
	return false
}

// resolveServiceAccounts maps an identity to ServiceAccounts. The admin login
// always maps to the dedicated admin ServiceAccount; OIDC identities map to
// every ServiceAccount in the list whose annotations match. Results are
// deterministic (sorted by name).
func resolveServiceAccounts(sas []*corev1.ServiceAccount, id *Identity) []corev1.ServiceAccount {
	if id == nil {
		return nil
	}
	var out []corev1.ServiceAccount
	if id.Provider == providerAdmin {
		for _, sa := range sas {
			if sa.Name == adminServiceAccountName {
				out = append(out, *sa)
			}
		}
		return out
	}
	for _, sa := range sas {
		if matchesIdentity(sa, id) {
			out = append(out, *sa)
		}
	}
	slices.SortFunc(out, func(a, b corev1.ServiceAccount) int { return strings.Compare(a.Name, b.Name) })
	return out
}
