package server

import (
	"net/http"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// signingKeyDTO is a public key Kokumi trusts.
type signingKeyDTO struct {
	// Name is "current" for the signing key, else the trusted key's name.
	Name      string `json:"name"`
	KeyID     string `json:"keyID"`
	PublicKey string `json:"publicKey"`
}

// signingResponse is the JSON body for GET /api/v1/signing.
type signingResponse struct {
	// Current is the key Kokumi signs with; absent until it is loaded.
	Current *signingKeyDTO `json:"current,omitempty"`
	// Ready reports whether the signing key is loaded and usable.
	Ready bool `json:"ready"`
	// Message explains why the key is not ready.
	Message string `json:"message,omitempty"`
	// TrustedKeys are the further keys Kokumi trusts for its own artifacts.
	TrustedKeys []signingKeyDTO `json:"trustedKeys"`
	// RequireSourceVerification reports whether every Product source must
	// verify.
	RequireSourceVerification bool `json:"requireSourceVerification"`
}

// handleGetSigning handles GET /api/v1/signing. It serves the public keys
// that verify Kokumi's signatures and needs no authentication: public keys
// are public, and anyone may verify an artifact with "cosign verify".
func handleGetSigning(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		kitchen, err := deps.kitchen()
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, "the Kitchen is not available: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, signingFromKitchen(kitchen))
	}
}

// signingFromKitchen reports the keys the Kitchen publishes. A trusted key
// that does not parse is listed without its key ID.
func signingFromKitchen(kitchen *deliveryv1alpha1.Kitchen) signingResponse {
	resp := signingResponse{TrustedKeys: []signingKeyDTO{}}

	if s := kitchen.Status.Signing; s != nil {
		resp.Current = &signingKeyDTO{Name: signing.CurrentKeyName, KeyID: s.KeyID, PublicKey: s.PublicKey}
	}
	if cond := meta.FindStatusCondition(kitchen.Status.Conditions, deliveryv1alpha1.ConditionTypeSigningKeyReady); cond != nil {
		resp.Ready = cond.Status == metav1.ConditionTrue
		if !resp.Ready {
			resp.Message = cond.Message
		}
	}

	for _, k := range signing.KitchenTrustedKeys(kitchen) {
		dto := signingKeyDTO{Name: k.Name, PublicKey: string(k.PEM)}
		if trust, err := signing.NewTrustSet(k); err == nil {
			dto.KeyID = trust.KeyIDs()[0]
		}
		resp.TrustedKeys = append(resp.TrustedKeys, dto)
	}

	if kitchen.Spec.Signing != nil {
		resp.RequireSourceVerification = kitchen.Spec.Signing.RequireSourceVerification
	}

	return resp
}
