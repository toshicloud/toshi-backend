// SPDX-License-Identifier: Apache-2.0

// Package storagetest is the conformance suite for storage.ObjectStore. Every
// implementation runs Run against a bucket it can write to; a case the store
// cannot serve is skipped by the name of the capability it lacks, never
// passed silently, and Run returns which cases ran and which were skipped.
package storagetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.toshicloud.io/backend/storage"
)

// Capabilities says what the store under test can do beyond what every
// store must, and where the suite may write.
type Capabilities struct {
	// Root is the key prefix the suite writes under, without a trailing
	// slash; empty is the bucket's root. The suite writes only under
	// Root/conformance-<random>/ and deletes that prefix when it ends. The
	// opener gives the provider the same root prefix, so Check's probe lands
	// under it too.
	Root string
	// Versioned is true when the bucket has versioning on, so Check must
	// refuse it with storage.ErrVersioned.
	Versioned bool
	// Lifecycle is true when the store takes lifecycle rules. Without it
	// CheckLifecycle must answer storage.ErrUnsupported and the cases that
	// write rules are skipped.
	Lifecycle bool
	// ReadLifecycle returns the bucket's lifecycle rules, each rule's ID
	// mapped to its prefix, read without the store under test. Nil skips
	// the cases that inspect which rules a call kept.
	ReadLifecycle func(ctx context.Context) (map[string]string, error)
	// PresignedTypeEnforced is true when the store refuses an upload
	// through a presigned URL whose Content-Type is not the signed one.
	PresignedTypeEnforced bool
	// HTTPClient makes the requests through presigned URLs; nil is a client
	// with a 30 second timeout.
	HTTPClient *http.Client
}

// Capability names, as a skip reports them.
const (
	CapLifecycle             = "lifecycle"
	CapReadLifecycle         = "read-lifecycle"
	CapPresignedTypeEnforced = "presigned-type-enforced"
)

// Skip is one case the store could not run.
type Skip struct {
	Case       string
	Capability string
	Reason     string
}

func (s Skip) String() string { return fmt.Sprintf("%s (%s: %s)", s.Case, s.Capability, s.Reason) }

// Report is what Run did: every case appears once, in Ran or in Skipped.
type Report struct {
	Ran     []string
	Skipped []Skip
}

// SkipList is each skipped case with its capability, in case order.
func (r Report) SkipList() []string {
	out := make([]string, 0, len(r.Skipped))
	for _, s := range r.Skipped {
		out = append(out, s.Case+" ("+s.Capability+")")
	}
	return out
}

func (r Report) String() string {
	skipped := "none"
	if len(r.Skipped) > 0 {
		parts := make([]string, len(r.Skipped))
		for i, s := range r.Skipped {
			parts[i] = s.String()
		}
		skipped = strings.Join(parts, "; ")
	}
	return fmt.Sprintf("ran %d: %s; skipped %d: %s", len(r.Ran), strings.Join(r.Ran, ", "), len(r.Skipped), skipped)
}

// Run runs every case against the store open returns, one subtest per case,
// logs the report and returns it. open is called once per case, and once
// more for the store that deletes the suite's prefix at the end.
func Run(t *testing.T, open func(t *testing.T) storage.ObjectStore, caps Capabilities) Report {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	base := "conformance-" + hex.EncodeToString(b[:])
	if caps.Root != "" {
		base = caps.Root + "/" + base
	}
	client := caps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	cleanup := open(t)
	t.Cleanup(func() {
		if _, err := cleanup.DeletePrefix(context.Background(), base+"/"); err != nil {
			t.Errorf("delete the suite's prefix %s/: %v", base, err)
		}
	})

	var report Report
	for _, c := range cases {
		var named *Skip
		var skipped bool
		t.Run(c.name, func(t *testing.T) {
			defer func() { skipped = t.Skipped() }()
			e := &env{
				t: t, store: open(t), caps: caps, http: client,
				prefix: base + "/" + c.name + "/",
				skip: func(capability, reason string) {
					named = &Skip{Case: c.name, Capability: capability, Reason: reason}
					t.Skipf("needs capability %s: %s", capability, reason)
				},
			}
			c.run(e)
		})
		switch {
		case skipped && named != nil:
			report.Skipped = append(report.Skipped, *named)
		case skipped:
			t.Errorf("case %s was skipped without naming a capability", c.name)
		default:
			report.Ran = append(report.Ran, c.name)
		}
	}
	t.Logf("object-store conformance: %s", report)
	return report
}

// Cases returns every case's name, in the order Run runs them.
func Cases() []string {
	names := make([]string, len(cases))
	for i, c := range cases {
		names[i] = c.name
	}
	return names
}

type testCase struct {
	name string
	// methods are the ObjectStore methods the case exercises.
	methods []string
	run     func(e *env)
}

var cases = []testCase{
	{"head-absent", []string{"Head"}, headAbsent},
	{"presign-put-upload", []string{"PresignPut", "Head"}, presignPutUpload},
	{"presign-put-refuses-length", []string{"PresignPut"}, presignPutRefusesLength},
	{"presign-put-refuses-type", []string{"PresignPut"}, presignPutRefusesType},
	{"presign-get", []string{"PresignGet"}, presignGet},
	{"list-pages", []string{"List"}, listPages},
	{"list-empty-and-absent-prefix", []string{"List"}, listEmptyAndAbsentPrefix},
	{"copy", []string{"Copy"}, copyObject},
	{"delete", []string{"Delete"}, deleteKeys},
	{"delete-prefix", []string{"DeletePrefix"}, deletePrefix},
	{"racing-writes-and-deletes", []string{"PresignPut", "Delete", "Head", "PresignGet"}, racingWritesAndDeletes},
	{"cancelled-context", []string{"Head", "List", "Copy", "Delete", "DeletePrefix", "Check", "ReconcileLifecycle", "CheckLifecycle"}, cancelledContext},
	{"reconcile-lifecycle", []string{"ReconcileLifecycle"}, reconcileLifecycle},
	{"reconcile-lifecycle-owner-names", []string{"ReconcileLifecycle"}, reconcileLifecycleOwnerNames},
	{"check-lifecycle", []string{"CheckLifecycle"}, checkLifecycle},
	{"check", []string{"Check"}, check},
	{"errors-distinct", []string{"Head", "Copy", "PresignPut", "PresignGet", "CheckLifecycle"}, errorsDistinct},
}

type env struct {
	t      *testing.T
	store  storage.ObjectStore
	caps   Capabilities
	http   *http.Client
	prefix string
	skip   func(capability, reason string)
}

func (e *env) key(name string) string { return e.prefix + name }

// send makes a presigned request with its headers, then header on top, and
// returns the status, the response headers and the body.
func (e *env) send(p storage.PresignedRequest, body []byte, header http.Header) (int, http.Header, []byte) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(e.t.Context(), p.Method, p.URL, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	for name, values := range p.Header {
		for _, v := range values {
			req.Header.Set(name, v)
		}
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Set(name, v)
		}
	}
	resp, err := e.http.Do(req)
	if err != nil {
		e.t.Fatalf("%s through a presigned URL: %v", p.Method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, data
}

// put uploads body through a presigned URL, as a client would.
func (e *env) put(key, contentType string, body []byte) {
	e.t.Helper()
	p, err := e.store.PresignPut(e.t.Context(), key, int64(len(body)), contentType, 5*time.Minute)
	if err != nil {
		e.t.Fatalf("PresignPut %s: %v", key, err)
	}
	if status, _, data := e.send(p, body, nil); status/100 != 2 {
		e.t.Fatalf("presigned put %s: HTTP %d: %.200s", key, status, data)
	}
}

// get downloads key through a presigned URL.
func (e *env) get(key, filename string) (int, http.Header, []byte) {
	e.t.Helper()
	p, err := e.store.PresignGet(e.t.Context(), key, 5*time.Minute, filename)
	if err != nil {
		e.t.Fatalf("PresignGet %s: %v", key, err)
	}
	return e.send(p, nil, nil)
}

func (e *env) wantAbsent(key string) {
	e.t.Helper()
	if _, err := e.store.Head(e.t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
		e.t.Errorf("Head %s = %v, want ErrNotFound", key, err)
	}
}

// listAll lists prefix page by page with limit, sending each Next back
// unchanged, and returns the keys in the order the pages gave them.
func (e *env) listAll(prefix string, limit int) []string {
	e.t.Helper()
	var keys []string
	token := ""
	for page := 0; ; page++ {
		if page > 100 {
			e.t.Fatalf("List %s did not end after %d pages", prefix, page)
		}
		got, err := e.store.List(e.t.Context(), prefix, token, limit)
		if err != nil {
			e.t.Fatalf("List %s: %v", prefix, err)
		}
		if len(got.Objects) > limit {
			e.t.Errorf("List %s with limit %d gave %d objects", prefix, limit, len(got.Objects))
		}
		for _, obj := range got.Objects {
			if !strings.HasPrefix(obj.Key, prefix) {
				e.t.Errorf("List %s gave %s, outside the prefix", prefix, obj.Key)
			}
			if obj.ETag == "" || obj.Size <= 0 {
				e.t.Errorf("List %s gave %+v, want an etag and a size", prefix, obj)
			}
			keys = append(keys, obj.Key)
		}
		if got.Next == "" {
			return keys
		}
		token = got.Next
	}
}

// staleETag is an etag no object in the suite has.
const staleETag = `"0123456789abcdef0123456789abcdef"`

func headAbsent(e *env) {
	_, err := e.store.Head(e.t.Context(), e.key("absent"))
	if !errors.Is(err, storage.ErrNotFound) {
		e.t.Fatalf("Head of an absent key = %v, want ErrNotFound", err)
	}
}

func presignPutUpload(e *env) {
	key := e.key("report.txt")
	e.put(key, "text/plain", []byte("hello"))
	info, err := e.store.Head(e.t.Context(), key)
	if err != nil {
		e.t.Fatal(err)
	}
	if info.Key != key || info.Size != 5 || info.ContentType != "text/plain" || info.ETag == "" || info.Modified.IsZero() {
		e.t.Errorf("Head = %+v, want key %s, size 5, text/plain, an etag and a modification time", info, key)
	}
}

func presignPutRefusesLength(e *env) {
	p, err := e.store.PresignPut(e.t.Context(), e.key("sized.bin"), 4, "text/plain", 5*time.Minute)
	if err != nil {
		e.t.Fatal(err)
	}
	if status, _, _ := e.send(p, []byte("12345"), nil); status/100 == 2 {
		e.t.Errorf("a body longer than the signed size was accepted: HTTP %d", status)
	}
	e.wantAbsent(e.key("sized.bin"))
}

func presignPutRefusesType(e *env) {
	if !e.caps.PresignedTypeEnforced {
		e.skip(CapPresignedTypeEnforced, "the store does not refuse an upload with another content type")
	}
	p, err := e.store.PresignPut(e.t.Context(), e.key("typed.bin"), 4, "text/plain", 5*time.Minute)
	if err != nil {
		e.t.Fatal(err)
	}
	if status, _, _ := e.send(p, []byte("1234"), http.Header{"Content-Type": {"application/x-other"}}); status/100 == 2 {
		e.t.Errorf("another content type was accepted: HTTP %d", status)
	}
	e.wantAbsent(e.key("typed.bin"))
}

func presignGet(e *env) {
	key := e.key("file.txt")
	e.put(key, "text/plain", []byte("contents"))

	status, header, body := e.get(key, "")
	if status != http.StatusOK || string(body) != "contents" {
		e.t.Errorf("GET = HTTP %d %q, want 200 contents", status, body)
	}
	if got := header.Get("Content-Disposition"); got != "" {
		e.t.Errorf("Content-Disposition without a filename = %q, want none", got)
	}

	status, header, body = e.get(key, "report.txt")
	if status != http.StatusOK || string(body) != "contents" {
		e.t.Errorf("GET with a filename = HTTP %d %q, want 200 contents", status, body)
	}
	if got := header.Get("Content-Disposition"); !strings.Contains(got, `filename="report.txt"`) {
		e.t.Errorf("Content-Disposition = %q, want an attachment named report.txt", got)
	}
}

func listPages(e *env) {
	prefix := e.key("list/")
	var want []string
	for i := range 5 {
		key := fmt.Sprintf("%sobj-%d", prefix, i)
		e.put(key, "text/plain", []byte("x"))
		want = append(want, key)
	}
	// A key that shares the prefix's text but not its slash.
	e.put(e.key("list-sibling"), "text/plain", []byte("x"))

	first := e.listAll(prefix, 2)
	sorted := slices.Clone(first)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		e.t.Fatalf("pages of 2 gave %v, want each of %v once", first, want)
	}
	if again := e.listAll(prefix, 2); !slices.Equal(again, first) {
		e.t.Errorf("a second listing gave %v, want the same order %v", again, first)
	}
}

func listEmptyAndAbsentPrefix(e *env) {
	e.put(e.key("present"), "text/plain", []byte("x"))
	page, err := e.store.List(e.t.Context(), "", "", 1)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(page.Objects) != 1 {
		e.t.Errorf("List of the whole bucket with limit 1 gave %d objects, want 1", len(page.Objects))
	}

	page, err = e.store.List(e.t.Context(), e.key("absent/"), "", 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(page.Objects) != 0 || page.Next != "" {
		e.t.Errorf("List of an absent prefix = %+v, want no objects and no Next", page)
	}
}

func copyObject(e *env) {
	ctx := e.t.Context()
	src := e.key("src.txt")
	e.put(src, "text/plain", []byte("one"))
	info, err := e.store.Head(ctx, src)
	if err != nil {
		e.t.Fatal(err)
	}

	if err := e.store.Copy(ctx, src, e.key("matched.txt"), info.ETag); err != nil {
		e.t.Fatalf("Copy with the source's etag: %v", err)
	}
	copied, err := e.store.Head(ctx, e.key("matched.txt"))
	if err != nil {
		e.t.Fatal(err)
	}
	if copied.Size != 3 || copied.ContentType != "text/plain" {
		e.t.Errorf("copy = %+v, want size 3 and text/plain", copied)
	}

	if err := e.store.Copy(ctx, src, e.key("stale.txt"), staleETag); !errors.Is(err, storage.ErrPrecondition) {
		e.t.Errorf("Copy with a stale etag = %v, want ErrPrecondition", err)
	}
	e.wantAbsent(e.key("stale.txt"))

	if err := e.store.Copy(ctx, e.key("absent.txt"), e.key("from-absent.txt"), ""); !errors.Is(err, storage.ErrNotFound) {
		e.t.Errorf("Copy of an absent source = %v, want ErrNotFound", err)
	}
	if err := e.store.Copy(ctx, src, e.key("unconditional.txt"), ""); err != nil {
		e.t.Errorf("Copy without an etag: %v", err)
	}
}

func deleteKeys(e *env) {
	a, b := e.key("a.txt"), e.key("b.txt")
	e.put(a, "text/plain", []byte("a"))
	e.put(b, "text/plain", []byte("b"))
	if err := e.store.Delete(e.t.Context(), a, b, e.key("absent.txt")); err != nil {
		e.t.Fatalf("Delete of two keys and an absent one: %v", err)
	}
	e.wantAbsent(a)
	e.wantAbsent(b)
	if err := e.store.Delete(e.t.Context()); err != nil {
		e.t.Errorf("Delete of no keys: %v", err)
	}
}

func deletePrefix(e *env) {
	prefix := e.key("dp/")
	for i := range 3 {
		e.put(fmt.Sprintf("%s%d", prefix, i), "text/plain", []byte("x"))
	}
	sibling := e.key("dp-sibling")
	e.put(sibling, "text/plain", []byte("x"))

	n, err := e.store.DeletePrefix(e.t.Context(), prefix)
	if err != nil || n != 3 {
		e.t.Fatalf("DeletePrefix = %d, %v, want 3", n, err)
	}
	if _, err := e.store.Head(e.t.Context(), sibling); err != nil {
		e.t.Errorf("a key outside the prefix went with it: %v", err)
	}
	if n, err := e.store.DeletePrefix(e.t.Context(), prefix); err != nil || n != 0 {
		e.t.Errorf("DeletePrefix of an emptied prefix = %d, %v, want 0", n, err)
	}
}

// racingWritesAndDeletes writes and deletes one key from several goroutines;
// each writer's body has its own length and byte, so the object left at the
// end, if any, must be one writer's whole body under its own etag.
func racingWritesAndDeletes(e *env) {
	const writers, rounds = 4, 5
	key := e.key("raced")
	ctx := e.t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds*2)
	for w := range writers {
		body := bytes.Repeat([]byte{byte('a' + w)}, w+1)
		wg.Go(func() {
			for range rounds {
				p, err := e.store.PresignPut(ctx, key, int64(len(body)), "application/octet-stream", 5*time.Minute)
				if err != nil {
					errs <- err
					return
				}
				if err := upload(ctx, e.http, p, body); err != nil {
					errs <- err
				}
				if err := e.store.Delete(ctx, key); err != nil {
					errs <- err
				}
				if err := upload(ctx, e.http, p, body); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		e.t.Errorf("racing call: %v", err)
	}

	info, err := e.store.Head(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if info.Size < 1 || info.Size > writers {
		e.t.Fatalf("Head after the race = %+v, want one writer's size", info)
	}
	status, header, body := e.get(key, "")
	want := bytes.Repeat([]byte{byte('a' + info.Size - 1)}, int(info.Size))
	if status != http.StatusOK || !bytes.Equal(body, want) || header.Get("ETag") != info.ETag {
		e.t.Errorf("after the race Head = %+v but GET = HTTP %d %q etag %s, want %q under the same etag",
			info, status, body, header.Get("ETag"), want)
	}
}

// upload sends body through p from a goroutine that must not call Fatal.
func upload(ctx context.Context, client *http.Client, p storage.PresignedRequest, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for name, values := range p.Header {
		for _, v := range values {
			req.Header.Set(name, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("presigned put: HTTP %d", resp.StatusCode)
	}
	return nil
}

func cancelledContext(e *env) {
	ctx, cancel := context.WithCancel(e.t.Context())
	cancel()
	calls := map[string]func() error{
		"Head":         func() error { _, err := e.store.Head(ctx, e.key("any")); return err },
		"List":         func() error { _, err := e.store.List(ctx, e.prefix, "", 10); return err },
		"Copy":         func() error { return e.store.Copy(ctx, e.key("a"), e.key("b"), "") },
		"Delete":       func() error { return e.store.Delete(ctx, e.key("any")) },
		"DeletePrefix": func() error { _, err := e.store.DeletePrefix(ctx, e.prefix); return err },
		"Check":        func() error { return e.store.Check(ctx) },
	}
	// Without the capability these answer ErrUnsupported before any request.
	if e.caps.Lifecycle {
		calls["ReconcileLifecycle"] = func() error { return e.store.ReconcileLifecycle(ctx, "cancelled", nil) }
		calls["CheckLifecycle"] = func() error { return e.store.CheckLifecycle(ctx) }
	}
	for _, name := range slices.Sorted(maps.Keys(calls)) {
		if err := calls[name](); !errors.Is(err, context.Canceled) {
			e.t.Errorf("%s with a cancelled context = %v, want context.Canceled wrapped", name, err)
		}
	}
}

// owners returns two owner names unique to this run whose rule prefixes do
// not overlap, and removes their rules when the case ends.
func (e *env) owners() (string, string) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		e.t.Fatal(err)
	}
	run := hex.EncodeToString(b[:])
	a, other := "conf"+run+"a", "conf"+run+"b"
	e.t.Cleanup(func() {
		for _, owner := range []string{a, other} {
			if err := e.store.ReconcileLifecycle(context.Background(), owner, nil); err != nil {
				e.t.Errorf("remove owner %s's rules: %v", owner, err)
			}
		}
	})
	return a, other
}

func reconcileLifecycle(e *env) {
	if !e.caps.Lifecycle {
		e.skip(CapLifecycle, "the store takes no lifecycle rules")
	}
	if e.caps.ReadLifecycle == nil {
		e.skip(CapReadLifecycle, "the bucket's rules cannot be read past the store")
	}
	ctx := e.t.Context()
	a, other := e.owners()
	rule := func(name, prefix string) []storage.LifecycleRule {
		return []storage.LifecycleRule{{Name: name, Prefix: e.key(prefix), ExpireAfterDays: 30}}
	}
	if err := e.store.ReconcileLifecycle(ctx, a, rule("keep", "first/")); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.ReconcileLifecycle(ctx, other, rule("keep", "other/")); err != nil {
		e.t.Fatal(err)
	}
	before, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.ReconcileLifecycle(ctx, a, rule("replaced", "second/")); err != nil {
		e.t.Fatal(err)
	}
	after, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}

	ownPrefix := "toshi-" + a + "-"
	for id, prefix := range before {
		if strings.HasPrefix(id, ownPrefix) {
			continue
		}
		if got, ok := after[id]; !ok || got != prefix {
			e.t.Errorf("rule %s (prefix %s) of another owner was not kept: %v", id, prefix, after)
		}
	}
	for id := range after {
		if strings.HasPrefix(id, ownPrefix) && id != ownPrefix+"replaced" {
			e.t.Errorf("rule %s of this owner survived its replacement", id)
		}
	}
	if got := after[ownPrefix+"replaced"]; got != e.key("second/") {
		e.t.Errorf("this owner's new rule has prefix %q, want %q", got, e.key("second/"))
	}

	// Removing every rule of both owners works even when no rule is left.
	for _, owner := range []string{a, other} {
		if err := e.store.ReconcileLifecycle(ctx, owner, nil); err != nil {
			e.t.Fatalf("remove owner %s's rules: %v", owner, err)
		}
	}
	left, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for id := range left {
		if strings.HasPrefix(id, ownPrefix) || strings.HasPrefix(id, "toshi-"+other+"-") {
			e.t.Errorf("rule %s survived its owner's removal", id)
		}
	}
}

// reconcileLifecycleOwnerNames: of two owners whose names extend one
// another, each keeps its rules when the other reconciles, and an owner
// whose name holds a hyphen, whose rule IDs another owner's prefix would
// cover, is refused and writes nothing.
func reconcileLifecycleOwnerNames(e *env) {
	if !e.caps.Lifecycle {
		e.skip(CapLifecycle, "the store takes no lifecycle rules")
	}
	if e.caps.ReadLifecycle == nil {
		e.skip(CapReadLifecycle, "the bucket's rules cannot be read past the store")
	}
	ctx := e.t.Context()
	short, _ := e.owners()
	long := short + "x"
	e.t.Cleanup(func() {
		if err := e.store.ReconcileLifecycle(context.Background(), long, nil); err != nil {
			e.t.Errorf("remove owner %s's rules: %v", long, err)
		}
	})
	rule := func(prefix string) []storage.LifecycleRule {
		return []storage.LifecycleRule{{Name: "keep", Prefix: e.key(prefix), ExpireAfterDays: 30}}
	}
	for _, owner := range []string{short, long} {
		if err := e.store.ReconcileLifecycle(ctx, owner, rule(owner+"/")); err != nil {
			e.t.Fatalf("ReconcileLifecycle(%s): %v", owner, err)
		}
	}
	for _, owner := range []string{short, long} {
		if err := e.store.ReconcileLifecycle(ctx, owner, rule(owner+"/")); err != nil {
			e.t.Fatalf("ReconcileLifecycle(%s) again: %v", owner, err)
		}
	}
	got, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, owner := range []string{short, long} {
		if id := "toshi-" + owner + "-keep"; got[id] != e.key(owner+"/") {
			e.t.Errorf("rule %s = prefix %q after both owners reconciled, want %q: %v", id, got[id], e.key(owner+"/"), got)
		}
	}

	hyphen := short + "-b"
	if err := e.store.ReconcileLifecycle(ctx, hyphen, rule("hyphen/")); err == nil {
		e.t.Errorf("ReconcileLifecycle(%s) = nil, want a refused owner", hyphen)
	}
	after, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	if !maps.Equal(got, after) {
		e.t.Errorf("a refused owner changed the rules from %v to %v", got, after)
	}
	if err := e.store.ReconcileLifecycle(ctx, "", nil); err == nil {
		e.t.Error("ReconcileLifecycle with no owner = nil, want a refused owner")
	}
}

func checkLifecycle(e *env) {
	ctx := e.t.Context()
	if !e.caps.Lifecycle {
		if err := e.store.CheckLifecycle(ctx); !errors.Is(err, storage.ErrUnsupported) {
			e.t.Errorf("CheckLifecycle without the capability = %v, want ErrUnsupported", err)
		}
		return
	}
	if e.caps.ReadLifecycle == nil {
		e.skip(CapReadLifecycle, "the bucket's rules cannot be read past the store")
	}
	a, _ := e.owners()
	if err := e.store.ReconcileLifecycle(ctx, a, []storage.LifecycleRule{{Name: "keep", Prefix: e.key("kept/"), ExpireAfterDays: 30}}); err != nil {
		e.t.Fatal(err)
	}
	before, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.CheckLifecycle(ctx); err != nil {
		e.t.Fatalf("CheckLifecycle: %v", err)
	}
	after, err := e.caps.ReadLifecycle(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	if !maps.Equal(before, after) {
		e.t.Errorf("CheckLifecycle changed the rules from %v to %v", before, after)
	}
}

func check(e *env) {
	err := e.store.Check(e.t.Context())
	if e.caps.Versioned {
		if !errors.Is(err, storage.ErrVersioned) {
			e.t.Fatalf("Check of a versioned bucket = %v, want ErrVersioned", err)
		}
		return
	}
	if err != nil {
		e.t.Fatalf("Check: %v", err)
	}
	probes := "_probe/"
	if e.caps.Root != "" {
		probes = e.caps.Root + "/" + probes
	}
	page, err := e.store.List(e.t.Context(), probes, "", 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(page.Objects) != 0 {
		e.t.Errorf("Check left probe objects behind: %+v", page.Objects)
	}
}

// errorsDistinct proves each sentinel is answered for its own failure and
// for no other, and that an invalid request matches none of them.
func errorsDistinct(e *env) {
	ctx := e.t.Context()
	sentinels := map[string]error{
		"ErrNotFound": storage.ErrNotFound, "ErrPrecondition": storage.ErrPrecondition,
		"ErrUnsupported": storage.ErrUnsupported, "ErrVersioned": storage.ErrVersioned,
	}
	want := func(what string, err error, only string) {
		e.t.Helper()
		if err == nil {
			e.t.Errorf("%s: no error", what)
			return
		}
		for name, sentinel := range sentinels {
			if is := errors.Is(err, sentinel); is != (name == only) {
				e.t.Errorf("%s = %v: errors.Is %s is %v", what, err, name, is)
			}
		}
	}

	_, err := e.store.Head(ctx, e.key("absent"))
	want("missing object", err, "ErrNotFound")

	src := e.key("src.txt")
	e.put(src, "text/plain", []byte("one"))
	want("stale etag", e.store.Copy(ctx, src, e.key("dst.txt"), staleETag), "ErrPrecondition")

	_, err = e.store.PresignPut(ctx, e.key("x"), 1, "text/plain", 0)
	want("presigned put with no ttl", err, "")
	_, err = e.store.PresignGet(ctx, src, -time.Second, "")
	want("presigned get with a negative ttl", err, "")

	if !e.caps.Lifecycle {
		want("lifecycle without the capability", e.store.CheckLifecycle(ctx), "ErrUnsupported")
	}
}
