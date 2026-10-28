// SPDX-License-Identifier: Apache-2.0

// Package keyprovider is the contract a key provider implements: a Wrapper
// encrypts small values, such as a workspace's data key, under a key that
// stays with the provider. A key provider's package registers its kind from
// its init with Register, and the program that serves keys builds the kind
// its configuration names. Subpackage keyprovidertest is the conformance
// suite every key provider passes.
package keyprovider
