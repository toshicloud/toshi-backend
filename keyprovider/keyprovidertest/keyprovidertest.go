// SPDX-License-Identifier: Apache-2.0

// Package keyprovidertest is the conformance suite every key provider
// passes: each key provider's tests call Run with its keyprovider.Wrapper.
package keyprovidertest

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"go.toshicloud.io/backend/keyprovider"
)

// Run checks that w wraps and unwraps as a key provider must: a value
// round-trips with the associated data it was wrapped under, a 32-byte and a
// 1-byte plaintext both, two values wrapped in a row each open to their own
// plaintext, and a value does not open, with keyprovider.ErrSealed, under other
// associated data or another key id. It only unwraps values it wrapped
// itself, so it is safe against a provider that holds real keys.
func Run(t *testing.T, w keyprovider.Wrapper) {
	t.Helper()
	aad := []byte("keyringtest-v1:tenant-a")

	t.Run("round trip", func(t *testing.T) {
		plaintext := random(t, 32)
		wrapped := wrap(t, w, aad, plaintext)
		if wrapped.Provider == "" || wrapped.KeyID == "" {
			t.Fatalf("Wrap gave provider %q and key id %q; both must be set", wrapped.Provider, wrapped.KeyID)
		}
		if bytes.Contains(wrapped.Ciphertext, plaintext) {
			t.Fatal("the ciphertext holds the plaintext")
		}
		unwrapEqual(t, w, aad, wrapped, plaintext)
	})

	t.Run("one byte", func(t *testing.T) {
		plaintext := random(t, 1)
		unwrapEqual(t, w, aad, wrap(t, w, aad, plaintext), plaintext)
	})

	t.Run("other associated data", func(t *testing.T) {
		wrapped := wrap(t, w, aad, random(t, 32))
		_, err := w.Unwrap(t.Context(), []byte("keyringtest-v1:tenant-b"), wrapped)
		if !errors.Is(err, keyprovider.ErrSealed) {
			t.Fatalf("Unwrap under other associated data = %v, want keyprovider.ErrSealed", err)
		}
	})

	t.Run("another key id", func(t *testing.T) {
		wrapped := wrap(t, w, aad, random(t, 32))
		wrapped.KeyID += "-other"
		_, err := w.Unwrap(t.Context(), aad, wrapped)
		if !errors.Is(err, keyprovider.ErrSealed) {
			t.Fatalf("Unwrap with key id %q = %v, want keyprovider.ErrSealed", wrapped.KeyID, err)
		}
	})

	t.Run("two keys in a row", func(t *testing.T) {
		first, second := random(t, 32), random(t, 32)
		wrappedFirst := wrap(t, w, aad, first)
		wrappedSecond := wrap(t, w, aad, second)
		if bytes.Equal(wrappedFirst.Ciphertext, wrappedSecond.Ciphertext) {
			t.Fatal("two plaintexts wrapped to the same ciphertext")
		}
		unwrapEqual(t, w, aad, wrappedFirst, first)
		unwrapEqual(t, w, aad, wrappedSecond, second)
	})
}

func wrap(t *testing.T, w keyprovider.Wrapper, aad, plaintext []byte) keyprovider.Wrapped {
	t.Helper()
	wrapped, err := w.Wrap(t.Context(), aad, bytes.Clone(plaintext))
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	return wrapped
}

func unwrapEqual(t *testing.T, w keyprovider.Wrapper, aad []byte, wrapped keyprovider.Wrapped, want []byte) {
	t.Helper()
	got, err := w.Unwrap(t.Context(), aad, wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Unwrap gave %d bytes that differ from the %d wrapped", len(got), len(want))
	}
}

func random(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
