// SPDX-License-Identifier: Apache-2.0

// Package launchertest is the conformance suite every cloud launcher passes:
// a launcher's tests call Run with a constructor that returns a fresh harness
// for each subtest.
package launchertest

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"go.toshicloud.io/backend/launcher"
)

// Harness is one implementation under test, each launcher against its own
// fake cloud.
type Harness struct {
	// Launcher is a launcher whose fake cloud's subnet routes through a NAT.
	Launcher launcher.Launcher
	// NAT is the same launcher as a NATEgress, on the same fake cloud.
	NAT launcher.NATEgress
	// NoNAT is the same implementation against a fake subnet with no NAT route.
	NoNAT launcher.NATEgress
	// DeniedNAT is the same implementation against a valid NAT whose read the
	// fake refuses.
	DeniedNAT launcher.NATEgress
	// Size is a runner size this host runs.
	Size string
	// Unsupported are runner sizes this host cannot run: an unknown name, and
	// a non-Linux size where this host rejects it.
	Unsupported []string
	// UnknownID is an instance id in this cloud's format that the cloud never
	// had.
	UnknownID string
	// Input returns a launch request the launcher accepts, for the runner
	// name.
	Input func(runner string) launcher.LaunchInput
}

// Run executes the cloud launcher conformance suite. Each subtest calls
// newHarness for a fresh harness.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()

	t.Run("Every size the host runs has a type, and one it does not has none", func(t *testing.T) {
		h := newHarness(t)

		typ, ok := h.Launcher.InstanceType(h.Size)
		if !ok || typ == "" {
			t.Fatalf("InstanceType(%q) = %q, %t; want a type", h.Size, typ, ok)
		}
		for _, size := range h.Unsupported {
			if typ, ok := h.Launcher.InstanceType(size); ok || typ != "" {
				t.Errorf("InstanceType(%q) = %q, %t; want no type", size, typ, ok)
			}
		}
	})

	t.Run("An empty cloud lists no instance", func(t *testing.T) {
		h := newHarness(t)

		got, err := h.Launcher.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got == nil {
			t.Fatal("List returned nil, want an empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("List returned %d instances of an empty cloud: %+v", len(got), got)
		}
	})

	t.Run("A launch is listed by its ID", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		inst, err := h.Launcher.Launch(ctx, h.Input("job-listed-1"))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		if inst.ID == "" {
			t.Fatal("Launch returned an instance with no ID")
		}
		list, err := h.Launcher.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !slices.ContainsFunc(list, func(i launcher.Instance) bool { return i.ID == inst.ID }) {
			t.Fatalf("List does not hold the launched instance %q: %+v", inst.ID, list)
		}
	})

	t.Run("The same runner launched twice is one instance", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		in := h.Input("job-twice-1")

		first, err := h.Launcher.Launch(ctx, in)
		if err != nil {
			t.Fatalf("first Launch: %v", err)
		}
		second, err := h.Launcher.Launch(ctx, in)
		if err != nil {
			t.Fatalf("second Launch: %v", err)
		}
		if second.ID != first.ID {
			t.Fatalf("second Launch = %q, want the first instance %q", second.ID, first.ID)
		}
		list, err := h.Launcher.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 || list[0].ID != first.ID {
			t.Fatalf("List = %+v, want the one instance %q", list, first.ID)
		}
	})

	t.Run("Terminate accepts no ids and ids the cloud does not hold", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		if err := h.Launcher.Terminate(ctx); err != nil {
			t.Fatalf("Terminate(): %v", err)
		}
		if err := h.Launcher.Terminate(ctx, h.UnknownID); err != nil {
			t.Fatalf("Terminate(%q), an instance the cloud never had: %v", h.UnknownID, err)
		}
		inst, err := h.Launcher.Launch(ctx, h.Input("job-terminated-1"))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		for i := range 2 {
			if err := h.Launcher.Terminate(ctx, inst.ID); err != nil {
				t.Fatalf("Terminate(%q) %d: %v", inst.ID, i+1, err)
			}
		}
	})

	t.Run("NAT addresses are sorted; only a missing route is ErrNoNAT", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		ips, err := h.NAT.NATAddresses(ctx)
		if err != nil {
			t.Fatalf("NATAddresses: %v", err)
		}
		if len(ips) == 0 {
			t.Fatal("NATAddresses returned no address")
		}
		prev, err := netip.ParseAddr(ips[0])
		if err != nil {
			t.Fatalf("NATAddresses = %v, holds %q, which is not an IP address", ips, ips[0])
		}
		for _, s := range ips[1:] {
			addr, err := netip.ParseAddr(s)
			if err != nil {
				t.Fatalf("NATAddresses = %v, holds %q, which is not an IP address", ips, s)
			}
			if addr.Compare(prev) <= 0 {
				t.Fatalf("NATAddresses = %v, want ascending addresses with no repeats", ips)
			}
			prev = addr
		}

		if _, err := h.NoNAT.NATAddresses(ctx); !errors.Is(err, launcher.ErrNoNAT) {
			t.Fatalf("NATAddresses without a NAT route: %v, want ErrNoNAT", err)
		}
		if _, err := h.DeniedNAT.NATAddresses(ctx); err == nil || errors.Is(err, launcher.ErrNoNAT) {
			t.Fatalf("NATAddresses whose read the cloud refuses: %v, want a refusal other than ErrNoNAT", err)
		}
	})

	t.Run("EgressEcho is empty while the guest has written none", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		inst, err := h.Launcher.Launch(ctx, h.Input("job-echo-1"))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		echo, err := h.NAT.EgressEcho(ctx, inst.ID)
		if err != nil || echo != "" {
			t.Fatalf("EgressEcho(%q) = %q, %v; want empty and no error", inst.ID, echo, err)
		}
	})

	t.Run("EgressEcho of an instance the cloud never had is empty", func(t *testing.T) {
		h := newHarness(t)

		echo, err := h.NAT.EgressEcho(context.Background(), h.UnknownID)
		if err != nil || echo != "" {
			t.Fatalf("EgressEcho(%q) = %q, %v; want empty and no error", h.UnknownID, echo, err)
		}
	})

	t.Run("Doctor gives named checks in known states and none fails", func(t *testing.T) {
		h := newHarness(t)

		checks := h.Launcher.Doctor(context.Background(), false)
		if len(checks) == 0 {
			t.Fatal("Doctor returned no checks")
		}
		for _, c := range checks {
			if c.Name == "" {
				t.Errorf("a check has no name: %+v", c)
			}
			switch c.Status {
			case launcher.CheckOK, launcher.CheckWarn, launcher.CheckSkipped:
			case launcher.CheckFail:
				t.Errorf("check %q failed: %s", c.Name, c.Detail)
			default:
				t.Errorf("check %q has status %q, want ok, warn, fail or skipped", c.Name, c.Status)
			}
		}
	})

	t.Run("A cancelled context fails Launch and List", func(t *testing.T) {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := h.Launcher.Launch(ctx, h.Input("job-cancelled-1")); err == nil {
			t.Error("Launch with a cancelled context succeeded")
		}
		if _, err := h.Launcher.List(ctx); err == nil {
			t.Error("List with a cancelled context succeeded")
		}
	})
}
