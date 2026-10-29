// SPDX-License-Identifier: Apache-2.0

package storage

import "errors"

// Errors returned by every ObjectStore and by Open. Implementations wrap
// them with context, so callers compare with errors.Is.
var (
	// ErrNotFound means the object or bucket is not there.
	ErrNotFound = errors.New("storage: not found")
	// ErrUnsupported means the provider's store cannot do the operation,
	// such as a lifecycle configuration without the capability.
	ErrUnsupported = errors.New("storage: not supported by this provider")
	// ErrModeNotServed means the provider row exists but this slice does
	// not serve its mode (local_disk).
	ErrModeNotServed = errors.New("storage: provider mode is not served yet")
	// ErrPrecondition means a copy's if-match did not hold: the source
	// changed after the caller read it.
	ErrPrecondition = errors.New("storage: precondition failed")
	// ErrVersioned means the bucket has versioning on, so a delete would
	// keep old versions and the sweep could not free space.
	ErrVersioned = errors.New("storage: bucket versioning is on: deletes would keep old versions")
	// ErrNoBackend means the build links no object storage backend:
	// object storage is off and no provider's bucket opens.
	ErrNoBackend = errors.New("this build has no object storage backend")
	// ErrOff means storage.kind is "none" (Off): object storage is off and
	// no provider's bucket opens.
	ErrOff = errors.New(`object storage is off: storage.kind is "none"`)
)
