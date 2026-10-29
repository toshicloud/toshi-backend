// SPDX-License-Identifier: Apache-2.0

package keyprovider

import (
	"context"
	"testing"

	"go.toshicloud.io/product/stability"
)

// nopWrapper is a key that wraps nothing; the registry only builds it.
type nopWrapper struct{}

func (nopWrapper) Wrap(context.Context, []byte, []byte) (Wrapped, error) { return Wrapped{}, nil }
func (nopWrapper) Unwrap(context.Context, []byte, Wrapped) ([]byte, error) {
	return nil, ErrSealed
}

func nopFactory(func(v any) error, Host) (Wrapper, error) { return nopWrapper{}, nil }

// registryOf returns a registry of the kinds given, each built by
// nopFactory.
func registryOf(kinds ...string) *Registry {
	var r Registry
	for _, kind := range kinds {
		r.Register(kind, stability.Of(stability.Beta), nopFactory)
	}
	return &r
}

// TestRegistryOpensByKind: a registered kind builds its key, an unregistered
// one is refused with the kinds there are, and an unset kind is the build's
// one kind or refused when there are two or more.
func TestRegistryOpensByKind(t *testing.T) {
	r := registryOf("transit", "aws_kms")
	if _, err := r.NewWrapper("transit", func(any) error { return nil }, Host{}); err != nil {
		t.Fatalf("open transit: %v", err)
	}
	if err := r.CheckKind("gcp_kms"); err == nil || err.Error() != `"gcp_kms" is not one of aws_kms, transit` {
		t.Fatalf("check of an unregistered kind = %v", err)
	}
	if err := r.CheckKind(""); err == nil || err.Error() != "not set; this build has aws_kms, transit: set one" {
		t.Fatalf("check of the unset kind with two = %v", err)
	}
	if got := r.DefaultKind(); got != "" {
		t.Fatalf("default with two = %q, want none", got)
	}

	one := registryOf("transit")
	if err := one.CheckKind(""); err != nil {
		t.Fatalf("check of the unset kind with one = %v", err)
	}
	if got := one.DefaultKind(); got != "transit" {
		t.Fatalf("default with one = %q, want transit", got)
	}

	if err := registryOf().CheckKind(""); err == nil || err.Error() != `"": no key provider kind is registered` {
		t.Fatalf("check with none = %v", err)
	}
}

// TestRegisterRefuses: registering a kind twice, or with a stability that
// does not pass its Check, is a programming error.
func TestRegisterRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		stability  stability.Stability
	}{
		{"twice", "transit", stability.Of(stability.Beta)},
		{"no stability", "aws_kms", stability.Stability{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := registryOf("transit")
			defer func() {
				if recover() == nil {
					t.Fatalf("Register of %s did not panic", tc.kind)
				}
			}()
			r.Register(tc.kind, tc.stability, nopFactory)
		})
	}
	if s, ok := registryOf("transit").KindStability("transit"); !ok || s != stability.Of(stability.Beta) {
		t.Fatalf("stability of transit = %v, %v; want beta", s, ok)
	}
}
