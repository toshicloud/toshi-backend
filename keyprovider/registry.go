// SPDX-License-Identifier: Apache-2.0

package keyprovider

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"go.toshicloud.io/product/stability"
)

// The key provider registry: each engine package registers its kind from its
// init, and the program that serves keys builds the kind its configuration
// names, so nothing outside the engines picks one by name.

// Factory builds a key provider from its table in the configuration: decode
// reads the table into the provider's own configuration struct.
type Factory func(decode func(v any) error) (Wrapper, error)

// Registry is a set of kinds and their factories. The package's own one,
// which Register fills from the engines' inits, serves the package's
// functions; a program that resolves kinds through an interface can hold
// another, such as a test with the kinds it needs. The zero Registry is
// empty and ready to use.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	stability map[string]stability.Stability
}

var kinds Registry

// Register makes factory the key provider engine of kind, the name of its
// table in the key provider's configuration, at stability s. An engine calls it
// from its package's init, so a binary has the kinds of the engines it
// links. It panics when kind is empty, s does not pass stability's Check,
// factory is nil or kind is registered already: each is a programming error.
func Register(kind string, s stability.Stability, factory Factory) {
	kinds.Register(kind, s, factory)
}

// NewWrapper builds the key of kind with its registered Factory, which reads
// its table through decode. A kind nobody registered is an error that names
// the kinds there are.
func NewWrapper(kind string, decode func(v any) error) (Wrapper, error) {
	return kinds.NewWrapper(kind, decode)
}

// CheckKind returns nil when kind is registered. The empty kind is an error
// unless the build registered exactly one kind, which DefaultKind names.
func CheckKind(kind string) error { return kinds.CheckKind(kind) }

// DefaultKind is the kind a file without a provider table means: the one
// kind the build registered. With none, or two or more, it is empty, and
// CheckKind asks for a table to be set.
func DefaultKind() string { return kinds.DefaultKind() }

// Kinds returns the registered kinds, sorted.
func Kinds() []string { return kinds.Kinds() }

// KindStability returns the stability kind registered with, and false for a
// kind nobody registered.
func KindStability(kind string) (stability.Stability, bool) { return kinds.KindStability(kind) }

// Register adds kind to r as the package's Register does.
func (r *Registry) Register(kind string, s stability.Stability, factory Factory) {
	if kind == "" {
		panic("keyprovider: Register with an empty kind")
	}
	if err := s.Check(); err != nil {
		panic(fmt.Sprintf("keyprovider: Register %q: %v", kind, err))
	}
	if factory == nil {
		panic(fmt.Sprintf("keyprovider: Register %q with a nil factory", kind))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[kind]; ok {
		panic(fmt.Sprintf("keyprovider: Register %q twice", kind))
	}
	if r.factories == nil {
		r.factories = make(map[string]Factory)
		r.stability = make(map[string]stability.Stability)
	}
	r.factories[kind] = factory
	r.stability[kind] = s
}

// KindStability is the package's KindStability for r.
func (r *Registry) KindStability(kind string) (stability.Stability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stability[kind]
	return s, ok
}

// NewWrapper is the package's NewWrapper for r.
func (r *Registry) NewWrapper(kind string, decode func(v any) error) (Wrapper, error) {
	if kind == "" {
		kind = r.DefaultKind()
	}
	if err := r.CheckKind(kind); err != nil {
		return nil, err
	}
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	return factory(decode)
}

// CheckKind is the package's CheckKind for r.
func (r *Registry) CheckKind(kind string) error {
	names := r.Kinds()
	switch {
	case kind == "" && len(names) == 1:
		return nil
	case kind == "" && len(names) == 0:
		return r.unknown(kind)
	case kind == "":
		return fmt.Errorf("not set; this build has %s: set one", strings.Join(names, ", "))
	case slices.Contains(names, kind):
		return nil
	}
	return r.unknown(kind)
}

// DefaultKind is the package's DefaultKind for r.
func (r *Registry) DefaultKind() string {
	if names := r.Kinds(); len(names) == 1 {
		return names[0]
	}
	return ""
}

// Kinds is the package's Kinds for r.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for kind := range r.factories {
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

// unknown is the one error for a kind nobody registered.
func (r *Registry) unknown(kind string) error {
	names := r.Kinds()
	if len(names) == 0 {
		return fmt.Errorf("%q: no key provider kind is registered", kind)
	}
	return fmt.Errorf("%q is not one of %s", kind, strings.Join(names, ", "))
}
