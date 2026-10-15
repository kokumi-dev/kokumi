package signing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ErrKeyNotReady reports that the Kitchen signing key does not exist yet or
// cannot be used.
var ErrKeyNotReady = errors.New("signing key not ready")

// State is a snapshot of the Kitchen's signing configuration.
type State struct {
	// Signer signs with the current key.
	Signer *Signer
	// Trust holds the current key and the Kitchen's trusted keys.
	Trust *TrustSet
	// RequireSourceVerification is the Kitchen's
	// spec.signing.requireSourceVerification.
	RequireSourceVerification bool
}

// ID identifies the trust the state establishes. It changes when a key is
// rotated, a trusted key is added or removed, or source verification becomes
// required or optional.
func (s State) ID() string {
	if s.Trust == nil {
		return ""
	}
	return fmt.Sprintf("%s/%t", s.Trust.ID(), s.RequireSourceVerification)
}

// Provider holds the Kitchen's signing state for the controllers that sign
// or verify. The Kitchen controller reloads it whenever the Kitchen or its
// key Secret changes; the first use loads it as well, so that controllers
// need not wait for the Kitchen controller after a restart.
type Provider struct {
	reader client.Reader
	// namespace holds the Kitchen; keyNamespace holds the signing key.
	namespace    string
	keyNamespace string

	// loadMu serializes loads. Decrypting the key runs scrypt, which costs
	// 32 MiB, so concurrent first uses must not each decrypt it.
	loadMu sync.Mutex
	// key and keyDigest cache the decrypted key and the digest of the Secret
	// data it was loaded from. Only loads hold loadMu and use them.
	key       *Key
	keyDigest []byte

	mu          sync.RWMutex
	loaded      bool
	state       State
	err         error
	subscribers []chan event.GenericEvent
}

// NewProvider returns a Provider that reads the Kitchen from namespace and its
// key Secret from keyNamespace with reader. Pass an uncached reader, so that a
// Secret the Kitchen controller has just created is seen.
func NewProvider(reader client.Reader, namespace, keyNamespace string) *Provider {
	return &Provider{reader: reader, namespace: namespace, keyNamespace: keyNamespace}
}

// NewStaticProvider returns a Provider with a fixed state, for tests.
func NewStaticProvider(state State) *Provider {
	return &Provider{loaded: true, state: state}
}

// State returns the current signing state, loading it on first use. It
// fails with an error wrapping ErrKeyNotReady while the key is missing or
// invalid.
func (p *Provider) State(ctx context.Context) (State, error) {
	p.mu.RLock()
	loaded, state, err := p.loaded, p.state, p.err
	p.mu.RUnlock()

	if loaded {
		return state, err
	}

	p.loadMu.Lock()
	defer p.loadMu.Unlock()

	// Another caller may have loaded the state while this one waited.
	p.mu.RLock()
	loaded, state, err = p.loaded, p.state, p.err
	p.mu.RUnlock()
	if loaded {
		return state, err
	}
	return p.load(ctx)
}

// Load reads the Kitchen and its key Secret, makes them the current state,
// and notifies the subscribers when the trust changed. A missing Kitchen
// trusts no keys besides the signing key.
func (p *Provider) Load(ctx context.Context) (State, error) {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	return p.load(ctx)
}

// load is Load; callers hold loadMu.
func (p *Provider) load(ctx context.Context) (State, error) {
	state, err := p.read(ctx)

	p.mu.Lock()
	prevOK, prevID := p.loaded && p.err == nil, p.state.ID()
	switch {
	case err == nil:
		p.loaded, p.state, p.err = true, state, nil
	case errors.Is(err, ErrKeyNotReady) || !prevOK:
		p.loaded, p.state, p.err = true, State{}, err
	default:
		// A transient read error keeps the last known good state.
	}
	state, err = p.state, p.err
	subscribers := p.subscribers
	p.mu.Unlock()

	if err == nil && (!prevOK || prevID != state.ID()) {
		for _, ch := range subscribers {
			select {
			case ch <- event.GenericEvent{Object: &deliveryv1alpha1.Kitchen{}}:
			default:
				// A notification is already pending.
			}
		}
	}

	return state, err
}

func (p *Provider) read(ctx context.Context) (State, error) {
	secret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{Namespace: p.keyNamespace, Name: deliveryv1alpha1.SigningKeySecretName}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return State{}, fmt.Errorf("%w: Secret %s/%s not found", ErrKeyNotReady, p.keyNamespace, deliveryv1alpha1.SigningKeySecretName)
		}
		return State{}, fmt.Errorf("reading signing key Secret: %w", err)
	}

	key, err := p.loadKey(secret.Data)
	if err != nil {
		return State{}, fmt.Errorf("%w: %w", ErrKeyNotReady, err)
	}

	kitchen := &deliveryv1alpha1.Kitchen{}
	if err := p.reader.Get(ctx, client.ObjectKey{Namespace: p.namespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen); err != nil {
		if !apierrors.IsNotFound(err) {
			return State{}, fmt.Errorf("reading Kitchen: %w", err)
		}
		kitchen = nil
	}

	signer, err := NewSigner(key)
	if err != nil {
		return State{}, err
	}

	trust, err := NewTrustSet(append([]TrustedPublicKey{{Name: CurrentKeyName, PEM: key.publicPEM}}, KitchenTrustedKeys(kitchen)...)...)
	if err != nil {
		return State{}, fmt.Errorf("%w: %w", ErrKeyNotReady, err)
	}

	return State{
		Signer:                    signer,
		Trust:                     trust,
		RequireSourceVerification: kitchen != nil && kitchen.Spec.Signing != nil && kitchen.Spec.Signing.RequireSourceVerification,
	}, nil
}

// loadKey returns the key in data, decrypting it only when data changed
// since the last load. Callers hold loadMu.
func (p *Provider) loadKey(data map[string][]byte) (*Key, error) {
	digest := sha256.New()
	for _, field := range []string{SecretPrivateKey, SecretPassword, SecretPublicKey} {
		sum := sha256.Sum256(data[field])
		digest.Write(sum[:])
	}

	if p.key != nil && bytes.Equal(p.keyDigest, digest.Sum(nil)) {
		return p.key, nil
	}

	key, err := LoadKey(data)
	if err != nil {
		p.key, p.keyDigest = nil, nil
		return nil, err
	}

	p.key, p.keyDigest = key, digest.Sum(nil)
	return key, nil
}

// Changes returns a channel that receives an event whenever Load makes a
// state with different trust current. Controllers watch it with
// source.Channel to verify again.
func (p *Provider) Changes() <-chan event.GenericEvent {
	ch := make(chan event.GenericEvent, 1)

	p.mu.Lock()
	p.subscribers = append(p.subscribers, ch)
	p.mu.Unlock()

	return ch
}
