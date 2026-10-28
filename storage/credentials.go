// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"time"
)

// Credentials are the keys a backend signs a provider's requests with.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	// Expires is when temporary credentials stop working; zero for
	// long-term keys.
	Expires time.Time
}

// CredentialSource turns a provider's credential reference into
// credentials. It never returns or logs a value: a failure names the file,
// the profile or the wrapping key, never the secret.
type CredentialSource interface {
	Resolve(ctx context.Context, p Provider) (Credentials, error)
}
