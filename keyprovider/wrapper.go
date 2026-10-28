// SPDX-License-Identifier: Apache-2.0

package keyprovider

import (
	"context"
	"errors"
)

// ErrSealed means wrapped bytes did not open: another provider or key
// wrapped them, or the bytes or the additional data differ from the wrap.
var ErrSealed = errors.New("keyring: wrapped bytes do not open")

// Wrapped is a value as a Wrapper wrapped it. Only the provider and key
// that wrapped it can unwrap it.
type Wrapped struct {
	// Provider names the provider kind, such as "local".
	Provider string
	// KeyID names the provider's key that wrapped the value, so a store
	// holding values wrapped by an earlier key can say which one it needs.
	KeyID string
	// Ciphertext is the wrapped value.
	Ciphertext []byte
}

// Wrapper wraps and unwraps small values under a key that stays with the
// provider. aad is bound to the wrap: Unwrap with other aad returns
// ErrSealed, so a caller binds a value to what it belongs to, such as its
// workspace.
type Wrapper interface {
	Wrap(ctx context.Context, aad, plaintext []byte) (Wrapped, error)
	Unwrap(ctx context.Context, aad []byte, w Wrapped) ([]byte, error)
}
