// SPDX-License-Identifier: Apache-2.0

// Package storage is the contract an object storage backend implements: the
// ObjectStore a provider's bucket is read and written through, the
// provider record a backend opens, and the registry a backend's package
// registers its kind in from its init. The control plane opens providers
// through the kind its configuration names and never depends on a
// particular backend. Subpackage storagetest is the conformance suite every
// ObjectStore passes.
package storage
