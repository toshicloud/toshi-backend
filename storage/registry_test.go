// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"go.toshicloud.io/product/stability"
)

func nopFactory(func(v any) error) (Backend, error) { return nil, nil }

var beta = stability.Of(stability.Beta)

// stubBackend is a backend the registry tests only compare for identity.
type stubBackend struct{}

func (*stubBackend) Open(context.Context, Provider) (ObjectStore, error) { return nil, nil }

// mustPanic runs f and returns what it panicked with, failing the test when
// it did not panic.
func mustPanic(t *testing.T, f func()) (got any) {
	t.Helper()
	defer func() {
		got = recover()
		if got == nil {
			t.Fatal("no panic")
		}
	}()
	f()
	return nil
}

func TestRegistryRefusesASecondRegister(t *testing.T) {
	var r registry
	r.register("a", beta, nopFactory)
	got := mustPanic(t, func() { r.register("a", beta, nopFactory) })
	if want := `storage: Register "a" twice`; got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}
}

func TestRegistryRefusesAnEmptyKindAndANilFactory(t *testing.T) {
	var r registry
	if got, want := mustPanic(t, func() { r.register("", beta, nopFactory) }), "storage: Register with an empty kind"; got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}
	if got, want := mustPanic(t, func() { r.register("a", beta, nil) }), `storage: Register "a" with a nil factory`; got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}
	if got := r.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v after refused registers, want none", got)
	}
}

func TestRegistryRefusesNoStability(t *testing.T) {
	var r registry
	got := mustPanic(t, func() { r.register("a", stability.Stability{}, nopFactory) })
	if want := `storage: Register "a": no stability`; got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}
}

func TestRegistryExposesStability(t *testing.T) {
	var r registry
	r.register("a", beta, nopFactory)
	if got, ok := r.stabilityOf("a"); !ok || got != beta {
		t.Fatalf("stabilityOf(a) = %+v, %v; want the registered beta", got, ok)
	}
	if _, ok := r.stabilityOf("b"); ok {
		t.Fatal("stabilityOf(b) found an unregistered kind")
	}
}

// TestRegistryNewBackendCallsTheFactoryWithDecode checks that open hands the
// factory the caller's decode and returns the backend the factory built.
func TestRegistryNewBackendCallsTheFactoryWithDecode(t *testing.T) {
	var r registry
	stub := &stubBackend{}
	var decoded string
	r.register("a", beta, func(decode func(v any) error) (Backend, error) {
		if err := decode(&decoded); err != nil {
			return nil, err
		}
		return stub, nil
	})
	got, err := r.open("a", func(v any) error {
		*v.(*string) = "config of a"
		return nil
	})
	if err != nil {
		t.Fatalf("open = %v, want no error", err)
	}
	if decoded != "config of a" {
		t.Fatalf("factory decoded %q, want %q", decoded, "config of a")
	}
	if got != stub {
		t.Fatalf("open = %v, want the factory's backend", got)
	}
}

func TestRegistryUnknownKindNamesTheKinds(t *testing.T) {
	var r registry
	if err := r.check("gcs"); err == nil || err.Error() != `"gcs": no object storage kind is registered` {
		t.Fatalf("check on an empty registry = %v", err)
	}
	r.register("s3", beta, nopFactory)
	r.register("azure", beta, nopFactory)
	if got := r.kinds(); !slices.Equal(got, []string{"azure", "s3"}) {
		t.Fatalf("kinds = %v, want [azure s3]", got)
	}
	want := `"gcs" is not one of azure, s3`
	if err := r.check("gcs"); err == nil || err.Error() != want {
		t.Fatalf("check = %v, want %q", err, want)
	}
	if _, err := r.open("gcs", nil); err == nil || err.Error() != want {
		t.Fatalf("open = %v, want %q", err, want)
	}
	if err := r.check("s3"); err != nil {
		t.Fatalf("check of a registered kind = %v", err)
	}
}

// fixedCredentials is a credential source the decode test hands over.
type fixedCredentials Credentials

func (c fixedCredentials) Resolve(context.Context, Provider) (Credentials, error) {
	return Credentials(c), nil
}

func TestOptionsDecode(t *testing.T) {
	hc := &http.Client{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	given := Options{
		Credentials:           fixedCredentials{AccessKeyID: "AKID"},
		HTTP:                  hc,
		Now:                   func() time.Time { return now },
		AllowPrivateEndpoints: true,
	}
	var got Options
	if err := OptionsDecode(given)(&got); err != nil {
		t.Fatalf("decode = %v, want no error", err)
	}
	if !got.AllowPrivateEndpoints {
		t.Fatal("AllowPrivateEndpoints = false, want true")
	}
	if got.HTTP != hc {
		t.Fatalf("HTTP = %v, want the client handed over", got.HTTP)
	}
	if got.Now == nil {
		t.Fatal("Now = nil, want the clock handed over")
	}
	if at := got.Now(); !at.Equal(now) {
		t.Fatalf("Now() = %v, want %v", at, now)
	}
	if got.Credentials != given.Credentials {
		t.Fatalf("Credentials = %+v, want the resolver handed over", got.Credentials)
	}
	err := OptionsDecode(given)(&struct{}{})
	if err == nil || !strings.Contains(err.Error(), "only *storage.Options") {
		t.Fatalf("decode of another value = %v, want an error naming *storage.Options", err)
	}
}

// storage.kind unset means the only registered kind; with none registered it
// is object storage off, whose Open returns ErrNoBackend; with two it must
// be set.
func TestRegistryDefaultKindAndOff(t *testing.T) {
	var r registry
	if got := r.defaultKind(); got != "" {
		t.Fatalf("defaultKind with none registered = %q, want empty", got)
	}
	if err := r.check(""); err != nil {
		t.Fatalf("check of the empty kind with none registered = %v", err)
	}
	backend, err := r.open("", nil)
	if err != nil {
		t.Fatalf("open of the empty kind with none registered = %v", err)
	}
	if _, err := backend.Open(context.Background(), Provider{}); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("off backend Open = %v, want ErrNoBackend", err)
	}

	r.register("s3", beta, nopFactory)
	if got := r.defaultKind(); got != "s3" {
		t.Fatalf("defaultKind with s3 registered = %q, want s3", got)
	}

	r.register("azure", beta, nopFactory)
	if got := r.defaultKind(); got != "" {
		t.Fatalf("defaultKind with two registered = %q, want empty", got)
	}
	want := `not set; this build has azure, s3: set one, or "none" to turn object storage off`
	if err := r.check(""); err == nil || err.Error() != want {
		t.Fatalf("check of the empty kind with two registered = %v, want %q", err, want)
	}
	if _, err := r.open("", nil); err == nil || err.Error() != want {
		t.Fatalf("open of the empty kind with two registered = %v, want %q", err, want)
	}
}

// storage.kind "none" turns object storage off whatever the build
// registered, and no backend can take that kind.
func TestRegistryOffKind(t *testing.T) {
	var r registry
	r.register("s3", beta, nopFactory)
	if err := r.check(Off); err != nil {
		t.Fatalf("check(%q) = %v", Off, err)
	}
	backend, err := r.open(Off, nil)
	if err != nil {
		t.Fatalf("open(%q) = %v", Off, err)
	}
	if _, err := backend.Open(context.Background(), Provider{}); !errors.Is(err, ErrOff) {
		t.Fatalf("Open with object storage off = %v, want ErrOff", err)
	}
	if got, want := mustPanic(t, func() { r.register(Off, beta, nopFactory) }), `storage: Register "none": the kind that turns object storage off`; got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}
}
