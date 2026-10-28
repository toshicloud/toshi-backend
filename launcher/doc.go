// SPDX-License-Identifier: Apache-2.0

// Package launcher is the contract a cloud launcher implements: the interface
// that starts, lists and terminates a cloud host's instances, its warm set and
// NAT extensions, the instance and launch request it works with, the runner
// user data a launch carries and the rows of its doctor report. A launcher's
// package registers its provider from its init with Register, and subpackage
// launchertest is the conformance suite every launcher passes.
package launcher
