// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PresignedRequest is one URL a client can use until Expires.
type PresignedRequest struct {
	Method  string
	URL     string
	Header  http.Header
	Expires time.Time
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string
	Modified    time.Time
}

// LifecycleRule is one rule Toshi asks a bucket to apply.
type LifecycleRule struct {
	Name            string
	Prefix          string
	ExpireAfterDays int
}

// CheckLifecycleOwner refuses an owner of lifecycle rules that is empty or
// holds a hyphen. A rule's ID is "toshi-"+owner+"-"+name, so its owner is
// the text between "toshi-" and the next hyphen: an owner "a-b" would make
// rules that owner "a" reads as its own. A provider ID (NewID) holds none.
func CheckLifecycleOwner(owner string) error {
	if owner == "" || strings.Contains(owner, "-") {
		return fmt.Errorf("storage: lifecycle owner %q must be non-empty and hold no hyphen", owner)
	}
	return nil
}

// ListPage is one page of a listing. Next is the provider's continuation
// token for the page after this one: opaque to Toshi, and empty on the last
// page.
type ListPage struct {
	Objects []ObjectInfo
	Next    string
}

// ObjectStore is one provider's bucket, as the control plane uses it.
type ObjectStore interface {
	// PresignPut returns a URL that uploads one object, at most size bytes
	// of contentType, until the ttl passes (at most one hour). The
	// signature covers the length and the type.
	PresignPut(ctx context.Context, key string, size int64, contentType string, ttl time.Duration) (PresignedRequest, error)
	// PresignGet returns a URL that downloads one object until the ttl
	// passes. A filename, when given, is the attachment name the response
	// carries.
	PresignGet(ctx context.Context, key string, ttl time.Duration, filename string) (PresignedRequest, error)
	// Head returns the object's size, type, etag and modification time, or
	// ErrNotFound.
	Head(ctx context.Context, key string) (ObjectInfo, error)
	// List returns up to limit objects under prefix, continuing from token,
	// which is the previous page's Next sent back unchanged.
	List(ctx context.Context, prefix, token string, limit int) (ListPage, error)
	// Copy copies src to dst, and fails with ErrPrecondition when ifMatch
	// is set and src's etag differs.
	Copy(ctx context.Context, src, dst, ifMatch string) error
	// Delete removes every key, or fails naming how many could not be
	// removed.
	Delete(ctx context.Context, keys ...string) error
	// DeletePrefix removes every object under prefix and returns how many
	// were removed.
	DeletePrefix(ctx context.Context, prefix string) (int, error)
	// ReconcileLifecycle keeps every rule of the bucket whose ID does not
	// start with "toshi-"+owner+"-" and replaces Toshi's with rules, which
	// are named "toshi-"+owner+"-"+rule.Name. An owner CheckLifecycleOwner
	// refuses is an error and writes nothing, so no owner's prefix covers
	// another owner's rules.
	ReconcileLifecycle(ctx context.Context, owner string, rules []LifecycleRule) error
	// CheckLifecycle reads the bucket's lifecycle rules and writes nothing,
	// so a provider that is not stored can prove the capability.
	CheckLifecycle(ctx context.Context) error
	// Check proves the bucket is usable: it heads the bucket, refuses a
	// versioned one, then uploads, heads and deletes a probe object.
	Check(ctx context.Context) error
}

// ObjectStreamer is implemented by an ObjectStore that can read and write
// an object's bytes itself, so the control plane can rewrite an upload
// before it is stored. It is optional: a store without it cannot complete
// an upload that must be rewritten, such as a browser trace.
type ObjectStreamer interface {
	// Get returns the object's body, or ErrNotFound. With ifMatch set it
	// fails with ErrPrecondition when the object's etag differs.
	Get(ctx context.Context, key, ifMatch string) (io.ReadCloser, error)
	// Put writes size bytes of body to key with contentType. body is read
	// twice: once to sign it and once to send it.
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
}
