// SPDX-License-Identifier: Apache-2.0

package backend

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// Version is this module's version, MAJOR.MINOR.PATCH, as VERSION holds it.
// It is raised when the contract or its conformance suite changes, once per
// release of the module, not with every change.
func Version() string { return strings.TrimSpace(version) }
