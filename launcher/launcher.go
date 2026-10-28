// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"context"
	"errors"
	"time"
)

// States of an instance, the same for every cloud.
const (
	// StatePending: created, not running yet.
	StatePending = "pending"
	// StateRunning: the machine runs; the runner starts or runs.
	StateRunning = "running"
	// StateStopped: the instance exists, stopped, and can be started again (a
	// pool host's warm instance).
	StateStopped = "stopped"
	// StateEnded: shutting down, stopped or gone; its runner is done.
	StateEnded = "ended"
)

// Instance is one of a cloud host's instances.
type Instance struct {
	// ID is the cloud's name for the instance: an EC2 instance ID, a GCE
	// instance name.
	ID string
	// State is StatePending, StateRunning, StateStopped or StateEnded.
	State string
	// RunnerName is the runner name of the job the instance was launched
	// for, from its tags or labels.
	RunnerName string
	// Size is the job's runner size.
	Size       string
	LaunchTime time.Time
	// Stopped reports an ended instance that still exists and must be
	// terminated: it holds its disk until then.
	Stopped bool
	// Interrupted reports a spot instance the cloud reclaimed.
	Interrupted bool
	// InterruptedUnknown reports an ended spot instance that is still
	// shutting down, where the cloud names a reclaim only afterwards:
	// Interrupted is not known yet.
	InterruptedUnknown bool
	// Warm reports a pool instance of the warm set that was never given a job:
	// it has no toshi:job tag and carries toshi:warm=true.
	Warm bool
	// Outdated reports a warm instance that was launched from another image,
	// or prepared with another runner, than the pool's configuration now: the
	// driver replaces it, one ready instance at a time.
	Outdated bool
	// Spot reports a spot instance.
	Spot bool
	// SpotRefused is the error code the cloud refused a spot instance with
	// when Launch fell back to on demand; empty otherwise.
	SpotRefused string
	// PublicIP is the instance's public IPv4 address, when it has one.
	PublicIP string
}

// Live reports whether the instance may still run a job.
func (i Instance) Live() bool {
	return i.State == StatePending || i.State == StateRunning
}

// LaunchInput is one job's instance.
type LaunchInput struct {
	// RunnerName is the job's runner name. It makes the launch idempotent:
	// launching the same runner twice returns the first instance.
	RunnerName string
	// Size is the runner size.
	Size string
	// UserData carries the JIT configuration.
	UserData UserData
	// Spot asks for a spot instance. When the cloud refuses one for want of
	// capacity, Launch launches on demand instead.
	Spot bool
}

// Launcher starts, lists and terminates one cloud host's instances.
type Launcher interface {
	// InstanceType returns the machine type for a runner size, or false for
	// a size the host cannot run.
	InstanceType(size string) (string, bool)
	Launch(ctx context.Context, in LaunchInput) (Instance, error)
	// List returns every instance this control plane launched for the
	// host, ended ones included while the cloud still shows them.
	List(ctx context.Context) ([]Instance, error)
	// Terminate ends instances; one already gone is not an error.
	Terminate(ctx context.Context, ids ...string) error
	// Doctor checks the host's cloud setup without launching anything
	// (toshi doctor --cloud); spot is whether the host runs spot
	// instances.
	Doctor(ctx context.Context, spot bool) []Check
}

// WarmLauncher launches and starts instances of a cloud pool's warm set: shared
// stopped instances the driver turns into job runners.
type WarmLauncher interface {
	// LaunchWarm launches a warm instance of the size: RunInstances with the
	// prepare user data, tags toshi:warm=true and no toshi:job,
	// ClientToken=token, shutdown behaviour STOP.
	LaunchWarm(ctx context.Context, size, token string) (Instance, error)
	// Start turns a stopped warm instance into the job's instance, in this
	// order: CreateTags toshi:job=<runner name> + Name; delete tag
	// toshi:warm; ModifyInstanceAttribute UserData = the job user data;
	// StartInstances. A failure at any step returns the error.
	Start(ctx context.Context, instanceID string, in LaunchInput) (Instance, error)
}

// NATEgress is what the launcher of a host whose instances leave through
// the workspace's NAT adds. *ec2.Launcher and *gce.Launcher
// implement it.
type NATEgress interface {
	// NATAddresses looks up the public addresses of the NAT the host's
	// subnet routes through, sorted. It fails with ErrNoNAT when the subnet
	// has no NAT route.
	NATAddresses(ctx context.Context) ([]string, error)
	// EgressEcho returns the address an instance's guest found through the
	// HTTPS echo, from the instance's serial console; empty while the
	// console shows none, and for an instance the cloud does not know.
	EgressEcho(ctx context.Context, instanceID string) (string, error)
}

// ErrNoNAT is NATAddresses' error for a subnet that does not route through
// a NAT; the error wrapping it says why.
var ErrNoNAT = errors.New("no NAT route")

// CheckStatus is the outcome of one Doctor check.
type CheckStatus string

const (
	// CheckOK means the check passed.
	CheckOK CheckStatus = "ok"
	// CheckWarn means the host may work but something is worth fixing.
	CheckWarn CheckStatus = "warn"
	// CheckFail means the host cannot launch jobs until this is fixed.
	CheckFail CheckStatus = "fail"
	// CheckSkipped means a prior check failed and this one was not run.
	CheckSkipped CheckStatus = "skipped"
)

// Check is one row of a Doctor report.
type Check struct {
	Name   string
	Status CheckStatus
	Detail string
}

// Report collects Doctor rows in a fixed order of names.
type Report struct {
	names  []string
	Checks []Check
}

// NewReport starts a report whose rows are names, in order.
func NewReport(names ...string) *Report {
	return &Report{names: names, Checks: make([]Check, 0, len(names))}
}

// Add appends rows.
func (r *Report) Add(checks ...Check) *Report {
	r.Checks = append(r.Checks, checks...)
	return r
}

// SkipRest marks every row not yet added as skipped for reason and returns
// the rows.
func (r *Report) SkipRest(reason string) []Check {
	for _, name := range r.names[min(len(r.Checks), len(r.names)):] {
		r.Checks = append(r.Checks, Check{Name: name, Status: CheckSkipped, Detail: reason})
	}
	return r.Checks
}

// CodedError is a call the cloud refused, with the cloud's error code
// (EC2's UnauthorizedOperation, GCE's ZONE_RESOURCE_POOL_EXHAUSTED) and
// message.
type CodedError interface {
	error
	ErrorCode() string
	ErrorMessage() string
}

// ErrorDetail is err's cloud error code and message when the cloud refused
// the call, else err's text.
func ErrorDetail(err error) string {
	var coded CodedError
	switch {
	case errors.As(err, &coded) && coded.ErrorMessage() != "":
		return coded.ErrorCode() + ": " + coded.ErrorMessage()
	case errors.As(err, &coded):
		return coded.ErrorCode()
	case err != nil:
		return err.Error()
	default:
		return ""
	}
}
