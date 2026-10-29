// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"go.toshicloud.io/product/stability"
)

// The object storage registry: each backend package registers its kind
// from its init, and the control plane opens providers through the kind
// storage.kind names, so nothing outside the backends picks one by name.

// Factory builds the object storage backend of one kind: decode fills an
// *Options with what the caller hands every backend.
type Factory func(decode func(v any) error) (Backend, error)

// Options is what the caller hands a backend: how it resolves a provider's
// credentials and makes its calls.
type Options struct {
	// Credentials resolves a provider's credentials; nil leaves the choice
	// to the backend.
	Credentials CredentialSource
	// HTTP makes the provider's calls; nil means one with a 30 second
	// timeout.
	HTTP *http.Client
	// Now is the clock requests are signed with; nil means time.Now.
	Now func() time.Time
	// AllowPrivateEndpoints lets a workspace's own provider use an http,
	// loopback or private endpoint (storage.allow_private_endpoints).
	AllowPrivateEndpoints bool
}

// Off is the kind that turns object storage off in any build, whatever it
// registered: its backend's Open returns ErrOff. No backend registers it.
const Off = "none"

// Backend opens the object store of a provider row.
type Backend interface {
	Open(ctx context.Context, p Provider) (ObjectStore, error)
}

// OptionsDecode is the decode that hands a backend o: it fills an *Options
// and refuses any other value.
func OptionsDecode(o Options) func(v any) error {
	return func(v any) error {
		opts, ok := v.(*Options)
		if !ok {
			return fmt.Errorf("storage: a backend asked for %T, and only *storage.Options is handed over", v)
		}
		*opts = o
		return nil
	}
}

// registry is the kinds and their factories. The package's one registry is
// filled at init and read when the control plane opens a provider.
type registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	stability map[string]stability.Stability
}

var backends registry

// Register makes factory the object storage backend of kind, at stability s.
// A backend calls it from its package's init, so a binary has the kinds of the
// backends it links. It panics when kind is empty, s does not pass stability's
// Check, factory is nil or kind is registered already: each is a programming
// error.
func Register(kind string, s stability.Stability, factory Factory) {
	backends.register(kind, s, factory)
}

// NewBackend builds a backend of kind with its registered Factory, which
// reads its configuration through decode. A kind nobody registered is an
// error that names the kinds there are. Off is object storage off, whose
// backend's Open returns ErrOff, and so is the empty kind in a build that
// registered none, whose backend's Open returns ErrNoBackend.
func NewBackend(kind string, decode func(v any) error) (Backend, error) {
	return backends.open(kind, decode)
}

// Open builds the backend of kind with NewBackend and opens p's object store
// with it. A kind nobody registered is an error that names the kinds there
// are.
func Open(ctx context.Context, kind string, decode func(v any) error, p Provider) (ObjectStore, error) {
	backend, err := NewBackend(kind, decode)
	if err != nil {
		return nil, err
	}
	return backend.Open(ctx, p)
}

// CheckBackendKind returns nil when NewBackend builds a backend of kind (a
// registered kind, Off, or the empty kind in a build that registered none),
// and otherwise the error NewBackend returns for it.
func CheckBackendKind(kind string) error { return backends.check(kind) }

// DefaultBackendKind is the kind storage.kind means when it is not set: the
// one kind the build registered, or the empty kind (object storage off) when
// it registered none. With two or more it is empty, and CheckBackendKind
// asks for one, or Off, to be set.
func DefaultBackendKind() string { return backends.defaultKind() }

// BackendKinds returns the registered kinds, sorted.
func BackendKinds() []string { return backends.kinds() }

// BackendStability returns the stability kind registered with, and false for
// a kind nobody registered.
func BackendStability(kind string) (stability.Stability, bool) { return backends.stabilityOf(kind) }

func (r *registry) register(kind string, s stability.Stability, factory Factory) {
	if kind == "" {
		panic("storage: Register with an empty kind")
	}
	if kind == Off {
		panic(fmt.Sprintf("storage: Register %q: the kind that turns object storage off", kind))
	}
	if err := s.Check(); err != nil {
		panic(fmt.Sprintf("storage: Register %q: %v", kind, err))
	}
	if factory == nil {
		panic(fmt.Sprintf("storage: Register %q with a nil factory", kind))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[kind]; ok {
		panic(fmt.Sprintf("storage: Register %q twice", kind))
	}
	if r.factories == nil {
		r.factories = make(map[string]Factory)
		r.stability = make(map[string]stability.Stability)
	}
	r.factories[kind] = factory
	r.stability[kind] = s
}

func (r *registry) stabilityOf(kind string) (stability.Stability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stability[kind]
	return s, ok
}

func (r *registry) open(kind string, decode func(v any) error) (Backend, error) {
	if err := r.check(kind); err != nil {
		return nil, err
	}
	switch kind {
	case "":
		return off{err: ErrNoBackend}, nil
	case Off:
		return off{err: ErrOff}, nil
	}
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	return factory(decode)
}

func (r *registry) check(kind string) error {
	names := r.kinds()
	switch {
	case kind == Off, kind == "" && len(names) == 0:
		return nil
	case kind == "":
		return fmt.Errorf("not set; this build has %s: set one, or %q to turn object storage off", strings.Join(names, ", "), Off)
	case slices.Contains(names, kind):
		return nil
	}
	return r.unknown(kind)
}

func (r *registry) defaultKind() string {
	if names := r.kinds(); len(names) == 1 {
		return names[0]
	}
	return ""
}

func (r *registry) kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for kind := range r.factories {
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

// off is the backend with object storage off: a build with no object
// storage backend (ErrNoBackend), or storage.kind Off (ErrOff).
type off struct{ err error }

func (o off) Open(context.Context, Provider) (ObjectStore, error) { return nil, o.err }

// unknown is the one error for a kind nobody registered.
func (r *registry) unknown(kind string) error {
	names := r.kinds()
	if len(names) == 0 {
		return fmt.Errorf("%q: no object storage kind is registered", kind)
	}
	return fmt.Errorf("%q is not one of %s", kind, strings.Join(names, ", "))
}
