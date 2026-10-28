// SPDX-License-Identifier: Apache-2.0

// Package backend is the root of the contracts Toshi's control plane opens
// its backends through: object storage (subpackage storage), cloud
// launchers (launcher) and key providers (keyprovider). Each subpackage
// holds the interface a backend implements, the registry a backend's
// package registers its kind in from its init, and a conformance suite
// every backend of that type passes. A backend built in another module
// requires only this one.
package backend
