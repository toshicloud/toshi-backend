// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.toshicloud.io/backend/keyprovider"
)

// Mode is how a provider's bucket is used.
type Mode string

const (
	// ModeOperator is the operator's own bucket, shared by every workspace
	// under a key per kind and workspace.
	ModeOperator Mode = "operator"
	// ModeWorkspaceBucket is a workspace's own bucket, used by that workspace only.
	ModeWorkspaceBucket Mode = "tenant_bucket"
	// ModeLocalDisk is a provider whose bytes the host agent serves from
	// local disk; accepted as a row, not served by this package yet.
	ModeLocalDisk Mode = "local_disk"
)

// Valid reports whether m is one of the modes.
func (m Mode) Valid() bool {
	return m == ModeOperator || m == ModeWorkspaceBucket || m == ModeLocalDisk
}

// Capabilities is what a provider's store can do and what its egress costs.
type Capabilities struct {
	// Lifecycle reports whether the bucket takes a lifecycle configuration.
	Lifecycle bool
	// GuestPresign reports whether a guest, not the control plane, may use
	// a presigned URL.
	GuestPresign bool
	// GlobalReplication reports whether the bucket is readable in every
	// region the fleet runs in.
	GlobalReplication bool
	// EgressPriceMicrosPerGB is what one gibibyte leaving the provider
	// costs, in millionths of the operator's currency; 0 is unknown or
	// free.
	EgressPriceMicrosPerGB int64
}

// CredentialRef says how to obtain a provider's credentials. The value is
// never stored here: a file reference and a wrapped secret are the two
// shapes, and a role may be assumed with either.
type CredentialRef struct {
	// File is the path of an AWS shared credentials file.
	File string
	// Profile names the section of File; empty means "default".
	Profile string
	// Wrapped is the credentials as the keyring wrapped them.
	Wrapped *keyprovider.Wrapped
	// RoleARN is a role assumed with the file's or the wrapped credentials.
	RoleARN string
	// ExternalID is the value the role's trust policy requires.
	ExternalID string
}

// Validate reports whether the reference is one the adapter can resolve:
// exactly one of File and Wrapped, a profile only with a file, and an
// external ID only with a role.
func (c CredentialRef) Validate() error {
	switch {
	case c.File == "" && c.Wrapped == nil:
		return fmt.Errorf("storage: a provider needs credentials_file or a wrapped credential")
	case c.File != "" && c.Wrapped != nil:
		return fmt.Errorf("storage: a provider takes credentials_file or a wrapped credential, not both")
	case c.Profile != "" && c.File == "":
		return fmt.Errorf("storage: a credentials profile needs a credentials file")
	case c.ExternalID != "" && c.RoleARN == "":
		return fmt.Errorf("storage: an external ID needs a role ARN")
	}
	return nil
}

// Provider is one row of the storage_providers table: a place Toshi keeps
// objects. WorkspaceID empty means the operator's own provider.
type Provider struct {
	ID             string
	WorkspaceID    string
	Name           string
	Mode           Mode
	Endpoint       string
	Region         string
	Bucket         string
	RootPrefix     string
	PathStyle      bool
	Caps           Capabilities
	Credentials    CredentialRef
	LastCheckAt    time.Time
	LastCheckError string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
	rootPattern = regexp.MustCompile(`^[a-z0-9-]+(/[a-z0-9-]+)*$`)
)

// Validate reports whether the provider row is one the store accepts. The
// endpoint must already be canonical (see CanonicalEndpoint); a handler that
// takes one from a person canonicalises it first, so two spellings of one
// endpoint cannot become two rows.
func (p Provider) Validate() error {
	if !p.Mode.Valid() {
		return fmt.Errorf("storage: unknown mode %q", p.Mode)
	}
	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("storage: name %q is not 1 to 63 of a-z, 0-9 and -", p.Name)
	}
	if p.Caps.EgressPriceMicrosPerGB < 0 {
		return fmt.Errorf("storage: egress price must not be negative")
	}
	if p.Mode != ModeLocalDisk {
		if p.Bucket == "" {
			return fmt.Errorf("storage: bucket is required")
		}
		if p.Region == "" {
			return fmt.Errorf("storage: region is required")
		}
	}
	if p.Endpoint != "" || p.Mode != ModeLocalDisk {
		canonical, err := CanonicalEndpoint(p.Endpoint)
		if err != nil {
			return err
		}
		if canonical != p.Endpoint {
			return fmt.Errorf("storage: endpoint %q is not canonical; it is %q", p.Endpoint, canonical)
		}
	}
	if p.RootPrefix != "" && !rootPattern.MatchString(p.RootPrefix) {
		return fmt.Errorf("storage: root_prefix %q is not a lowercase path without a leading or trailing slash", p.RootPrefix)
	}
	// A local_disk provider's bytes are the host agent's, so it needs no
	// credential; one given is still checked.
	if p.Mode == ModeLocalDisk && p.Credentials == (CredentialRef{}) {
		return nil
	}
	return p.Credentials.Validate()
}

// CanonicalEndpoint returns the one spelling of an S3 endpoint Toshi stores
// for raw: the host lower-cased, no default port, no trailing slash, and
// nothing but scheme, host and port. https is the only scheme except for a
// loopback host, which may use http. A path other than "/", a query, a
// fragment and userinfo are refused rather than dropped: an endpoint that
// names more than the bucket's home is a mistake, not an alias. A refusal
// never repeats raw, which may hold a password or a signed query.
func CanonicalEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("storage: the endpoint is not a URL")
	}
	if u.User != nil {
		return "", errors.New("storage: the endpoint must not carry credentials")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("storage: the endpoint must not carry a query")
	}
	if u.Fragment != "" {
		return "", errors.New("storage: the endpoint must not carry a fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("storage: the endpoint must not carry a path")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("storage: the endpoint is not a URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopbackHost(host) {
			return "", errors.New("storage: the endpoint must be https (http only for a loopback address)")
		}
	default:
		return "", errors.New("storage: the endpoint must be https (http only for a loopback address)")
	}

	// The port is parsed and written back in decimal: "0443" and "443" reach
	// the same socket, so they must be one stored endpoint.
	port := ""
	if raw := u.Port(); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || n == 0 {
			return "", errors.New("storage: the endpoint has an invalid port")
		}
		def := uint64(443)
		if u.Scheme == "http" {
			def = 80
		}
		if n != def {
			port = strconv.FormatUint(n, 10)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("storage: the endpoint has an invalid port")
	}
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(host, "%") {
			return "", errors.New("storage: the endpoint must not name an IPv6 zone")
		}
		host = ip.String()
	} else if !dnsHost(host) {
		return "", errors.New("storage: the endpoint's host is not a DNS name or an IP address")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	out := u.Scheme + "://" + host
	if port != "" {
		out += ":" + port
	}
	return out, nil
}

// dnsHost reports whether host is a DNS name in the one spelling Toshi
// stores: letters, digits and hyphens in non-empty labels, no trailing dot,
// and a last label that is not all digits (so "10.1" or "0x7f.1", which some
// resolvers read as addresses, are refused rather than stored as names).
func dnsHost(host string) bool {
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

func loopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
