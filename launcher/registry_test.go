// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.toshicloud.io/product/stability"
)

// The launcher registry's refusals, the decode it hands a factory and the
// names its errors list.

func TestRegisterRefuses(t *testing.T) {
	nop := func(func(v any) error) (Launcher, error) { return nil, nil }
	beta := stability.Of(stability.Beta)
	for name, register := range map[string]func(r *registry){
		"empty provider": func(r *registry) { r.register("", beta, nop, nil) },
		"no stability":   func(r *registry) { r.register("aws", stability.Stability{}, nop, nil) },
		"nil factory":    func(r *registry) { r.register("aws", beta, nil, nop) },
		"twice":          func(r *registry) { r.register("aws", beta, nop, nil); r.register("aws", beta, nop, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("register did not panic")
				}
			}()
			register(&registry{})
		})
	}
}

func TestRegisterRefusesNoStability(t *testing.T) {
	nop := func(func(v any) error) (Launcher, error) { return nil, nil }
	defer func() {
		if got, want := recover(), `cloud: Register "aws": no stability`; got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	var r registry
	r.register("aws", stability.Stability{}, nop, nil)
}

func TestRegistryExposesStability(t *testing.T) {
	nop := func(func(v any) error) (Launcher, error) { return nil, nil }
	beta := stability.Of(stability.Beta)
	var r registry
	r.register("aws", beta, nop, nil)
	if got, ok := r.stabilityOf("aws"); !ok || got != beta {
		t.Fatalf("stabilityOf(aws) = %+v, %v; want the registered beta", got, ok)
	}
}

func TestRegistryOpen(t *testing.T) {
	type section struct{ Endpoint string }
	var got struct {
		section section
		host    Host
		pool    Pool
		poolErr error
	}
	factory := func(decode func(v any) error) (Launcher, error) {
		if err := decode(&got.section); err != nil {
			return nil, err
		}
		if err := decode(&got.host); err != nil {
			return nil, err
		}
		got.poolErr = decode(&got.pool)
		return nil, nil
	}
	beta := stability.Of(stability.Beta)
	var r registry
	r.register("gcp", beta, factory, nil)
	r.register("aws", beta, factory, factory)
	fill := func(v any) error { v.(*section).Endpoint = "http://fake"; return nil }

	if _, err := r.open("aws", Host{HostID: "h1"}, &Pool{Name: "win"}, fill); err != nil {
		t.Fatal(err)
	}
	if got.section.Endpoint != "http://fake" || got.host.HostID != "h1" || got.pool.Name != "win" || got.poolErr != nil {
		t.Fatalf("pool factory read %+v", got)
	}
	if _, err := r.open("gcp", Host{HostID: "h2"}, nil, fill); err != nil {
		t.Fatal(err)
	}
	if got.host.HostID != "h2" || got.poolErr == nil {
		t.Fatalf("host factory read %+v, want a host and no pool", got)
	}

	sectionErr := errors.New("bad section")
	if _, err := r.open("aws", Host{}, nil, func(any) error { return sectionErr }); !errors.Is(err, sectionErr) {
		t.Fatalf("open with a bad section: %v, want %v", err, sectionErr)
	}
	if names := r.names(false); !slices.Equal(names, []string{"aws", "gcp"}) {
		t.Fatalf("providers %v, want [aws gcp]", names)
	}
	for _, c := range []struct {
		provider string
		pools    bool
		want     string
	}{
		{"azure", false, `"azure" is not one of aws, gcp`},
		{"gcp", true, `"gcp" is not one of aws`},
	} {
		if err := r.check(c.provider, c.pools); err == nil || err.Error() != c.want {
			t.Errorf("check(%q, pools %v) = %v, want %q", c.provider, c.pools, err, c.want)
		}
		_, err := r.open(c.provider, Host{}, map[bool]*Pool{true: {}}[c.pools], fill)
		if err == nil || err.Error() != c.want {
			t.Errorf("open(%q, pools %v) = %v, want %q", c.provider, c.pools, err, c.want)
		}
	}
	var empty registry
	if err := empty.check("aws", true); err == nil || err.Error() != `"aws": no cloud pool provider is registered` {
		t.Fatalf("empty registry: %v", err)
	}
}

func TestRegistryCheckAccess(t *testing.T) {
	nop := func(func(v any) error) (Launcher, error) { return nil, nil }
	beta := stability.Of(stability.Beta)
	var r registry
	r.register("aws", beta, nop, nil)
	r.register("gcp", beta, nop, nil)
	var decoded string
	r.registerAccessCheck("aws", func(_ context.Context, decode func(v any) error) error {
		return decode(&decoded)
	})
	section := func(v any) error {
		*v.(*string) = "section"
		return nil
	}

	if err := r.checkAccess(t.Context(), "aws", section); err != nil || decoded != "section" {
		t.Fatalf("checkAccess(aws) = %v, decoded %q; want nil and the section", err, decoded)
	}
	if err := r.checkAccess(t.Context(), "gcp", section); err != nil {
		t.Fatalf("checkAccess of a provider without a check = %v, want nil", err)
	}
	if err := r.checkAccess(t.Context(), "azure", section); err == nil || err.Error() != `"azure" is not one of aws, gcp` {
		t.Fatalf("checkAccess of an unregistered provider = %v", err)
	}
	for name, register := range map[string]func(){
		"unregistered provider": func() { r.registerAccessCheck("azure", func(context.Context, func(v any) error) error { return nil }) },
		"nil check":             func() { r.registerAccessCheck("gcp", nil) },
		"twice":                 func() { r.registerAccessCheck("aws", func(context.Context, func(v any) error) error { return nil }) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("registerAccessCheck did not panic")
				}
			}()
			register()
		})
	}
}
