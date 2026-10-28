// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"go.toshicloud.io/product/stability"
)

// The launcher registry: each cloud's launcher package registers its
// provider from its init, and the cloud manager opens the provider a cloud
// host or a cloud pool names, so it builds launchers without naming the
// clouds it knows.

// Factory builds a launcher: decode fills the provider's own configuration
// from its [cloud.<provider>] section, as a store backend's Factory reads
// [store], and fills a *Host with the host the launcher serves and, for a pool
// factory, a *Pool with the pool.
type Factory func(decode func(v any) error) (Launcher, error)

// Host is a cloud host as its launcher reads it. The cloud manager builds it
// from the stored cloud host, which this package cannot import.
type Host struct {
	// ControlPlaneID names this control plane in the instances' tags, so two
	// control planes sharing an account never reap each other's instances.
	ControlPlaneID string
	// HostID and Workspace are the cloud host's ID and owner; a pool host has
	// no owner.
	HostID    string
	Workspace string
	// Region is the region the instances run in; on GCP the zone's region.
	Region string
	// RoleARN and ExternalID name the role in the workspace's account (AWS);
	// on GCP ExternalID is what the project metadata must name.
	RoleARN    string
	ExternalID string
	// Image is the machine image the instances boot.
	Image string
	// SubnetID is the subnet (AWS) or subnetwork (GCP) to launch into; empty
	// means a default subnet or network.
	SubnetID string
	// Project, Zone and ServiceAccount are where a GCP host's instances run
	// and the workspace's service account the control plane impersonates.
	Project        string
	Zone           string
	ServiceAccount string
	// SecurityGroupIDs are the instances' security groups (AWS); empty means
	// the VPC's default group.
	SecurityGroupIDs []string
	// InstanceTypes maps a runner size to an instance type, overriding the
	// launcher's defaults.
	InstanceTypes map[string]string
	// NAT launches without public addresses: the workspace's NAT carries the
	// traffic.
	NAT bool
}

// Pool is a cloud pool as the launcher of its pool host reads it: the pool's
// [[cloud_pools]] entry, less what its Host carries.
type Pool struct {
	// Name is the pool's name; OS is its runners' OS.
	Name string
	OS   string
	// RunnerURL and RunnerSHA256 locate the actions-runner archive the
	// pool's instances install.
	RunnerURL    string
	RunnerSHA256 string
	// VolumeGiB is the size of the instances' work volume.
	VolumeGiB int
	// CredentialsFile and Profile are the pool's shared credentials file and
	// its profile; InstanceRole uses the control plane's instance role
	// instead.
	CredentialsFile string
	Profile         string
	InstanceRole    bool
}

// registry is the providers and their factories. The package's one registry
// is filled at init and read when the control plane starts.
type registry struct {
	mu        sync.RWMutex
	hosts     map[string]Factory
	pools     map[string]Factory
	access    map[string]AccessCheck
	stability map[string]stability.Stability
}

var providers registry

// Register makes factory the launcher factory of provider's cloud hosts and
// pool, unless nil, the launcher factory of its cloud pool hosts, at stability
// s. A launcher package calls it from its init, so a binary has the providers
// of the packages it links. It panics when provider is empty, s does not pass
// stability's Check, factory is nil or provider is registered already: each is
// a programming error.
func Register(provider string, s stability.Stability, factory, pool Factory) {
	providers.register(provider, s, factory, pool)
}

// AccessCheck checks the control plane's own access to a provider's cloud
// before the cloud manager starts: decode fills the provider's
// configuration from its [cloud.<provider>] section.
type AccessCheck func(ctx context.Context, decode func(v any) error) error

// RegisterAccessCheck makes check the access check of provider, which its
// launcher package registered already; it calls this from the same init. It
// panics when provider is not registered, check is nil or provider has one
// already: each is a programming error.
func RegisterAccessCheck(provider string, check AccessCheck) {
	providers.registerAccessCheck(provider, check)
}

// CheckAccess runs the access check provider registered, with section as its
// decode. A provider without one has nothing to check; one nobody
// registered is the error Open returns for it.
func CheckAccess(ctx context.Context, provider string, section func(v any) error) error {
	return providers.checkAccess(ctx, provider, section)
}

// Open builds the launcher of a cloud host on provider with its registered
// Factory, which reads the provider's configuration through section. A
// provider nobody registered is an error that names the providers there are.
func Open(provider string, h Host, section func(v any) error) (Launcher, error) {
	return providers.open(provider, h, nil, section)
}

// OpenPool builds the launcher of pool's pool host on provider with the
// provider's registered pool Factory. A provider that registered none is an
// error that names the providers that did.
func OpenPool(provider string, pool Pool, h Host, section func(v any) error) (Launcher, error) {
	return providers.open(provider, h, &pool, section)
}

// CheckProvider returns nil when provider is registered, and otherwise the
// error Open returns for it.
func CheckProvider(provider string) error { return providers.check(provider, false) }

// CheckPoolProvider returns nil when provider registered a pool factory, and
// otherwise the error OpenPool returns for it.
func CheckPoolProvider(provider string) error { return providers.check(provider, true) }

// Providers returns the registered providers, sorted.
func Providers() []string { return providers.names(false) }

// PoolProviders returns the providers that registered a pool factory, sorted.
func PoolProviders() []string { return providers.names(true) }

// ProviderStability returns the stability provider registered with, and false
// for a provider nobody registered.
func ProviderStability(provider string) (stability.Stability, bool) {
	return providers.stabilityOf(provider)
}

func (r *registry) register(provider string, s stability.Stability, factory, pool Factory) {
	if provider == "" {
		panic("cloud: Register with an empty provider")
	}
	if err := s.Check(); err != nil {
		panic(fmt.Sprintf("cloud: Register %q: %v", provider, err))
	}
	if factory == nil {
		panic(fmt.Sprintf("cloud: Register %q with a nil factory", provider))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[provider]; ok {
		panic(fmt.Sprintf("cloud: Register %q twice", provider))
	}
	if r.hosts == nil {
		r.hosts = make(map[string]Factory)
		r.pools = make(map[string]Factory)
		r.stability = make(map[string]stability.Stability)
	}
	r.hosts[provider] = factory
	r.stability[provider] = s
	if pool != nil {
		r.pools[provider] = pool
	}
}

func (r *registry) stabilityOf(provider string) (stability.Stability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stability[provider]
	return s, ok
}

func (r *registry) registerAccessCheck(provider string, check AccessCheck) {
	if check == nil {
		panic(fmt.Sprintf("cloud: RegisterAccessCheck %q with a nil check", provider))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[provider]; !ok {
		panic(fmt.Sprintf("cloud: RegisterAccessCheck %q before Register", provider))
	}
	if _, ok := r.access[provider]; ok {
		panic(fmt.Sprintf("cloud: RegisterAccessCheck %q twice", provider))
	}
	if r.access == nil {
		r.access = make(map[string]AccessCheck)
	}
	r.access[provider] = check
}

func (r *registry) checkAccess(ctx context.Context, provider string, section func(v any) error) error {
	r.mu.RLock()
	_, ok := r.hosts[provider]
	check := r.access[provider]
	r.mu.RUnlock()
	if !ok {
		return r.unknown(provider, false)
	}
	if check == nil {
		return nil
	}
	return check(ctx, section)
}

func (r *registry) open(provider string, h Host, pool *Pool, section func(v any) error) (Launcher, error) {
	r.mu.RLock()
	factory, ok := r.factories(pool != nil)[provider]
	r.mu.RUnlock()
	if !ok {
		return nil, r.unknown(provider, pool != nil)
	}
	return factory(func(v any) error {
		switch v := v.(type) {
		case *Host:
			*v = h
			return nil
		case *Pool:
			if pool == nil {
				return fmt.Errorf("cloud %s: a cloud host's launcher has no pool", provider)
			}
			*v = *pool
			return nil
		}
		return section(v)
	})
}

func (r *registry) check(provider string, pools bool) error {
	r.mu.RLock()
	_, ok := r.factories(pools)[provider]
	r.mu.RUnlock()
	if !ok {
		return r.unknown(provider, pools)
	}
	return nil
}

// factories is the host or the pool factories; the caller holds r.mu.
func (r *registry) factories(pools bool) map[string]Factory {
	if pools {
		return r.pools
	}
	return r.hosts
}

func (r *registry) names(pools bool) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	factories := r.factories(pools)
	out := make([]string, 0, len(factories))
	for provider := range factories {
		out = append(out, provider)
	}
	slices.Sort(out)
	return out
}

// unknown is the one error for a provider nobody registered, or that
// registered no pool factory.
func (r *registry) unknown(provider string, pools bool) error {
	names := r.names(pools)
	if len(names) == 0 {
		if pools {
			return fmt.Errorf("%q: no cloud pool provider is registered", provider)
		}
		return fmt.Errorf("%q: no cloud provider is registered", provider)
	}
	return fmt.Errorf("%q is not one of %s", provider, strings.Join(names, ", "))
}
