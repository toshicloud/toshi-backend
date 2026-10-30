// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"
	"testing"
)

// A local_disk provider needs no credential, and one it is given is still
// checked; the other modes still need one.
func TestProviderValidateCredential(t *testing.T) {
	bucket := Provider{Name: "bucket", Mode: ModeOperator, Endpoint: "https://s3.example.com", Region: "us-east-1", Bucket: "objects"}
	cases := []struct {
		name string
		p    Provider
		want string
	}{
		{"local_disk without a credential", Provider{Name: "disk", Mode: ModeLocalDisk}, ""},
		{"local_disk with a credentials file", Provider{Name: "disk", Mode: ModeLocalDisk, Credentials: CredentialRef{File: "/etc/toshi/aws"}}, ""},
		{"local_disk with a profile and no file", Provider{Name: "disk", Mode: ModeLocalDisk, Credentials: CredentialRef{Profile: "objects"}}, "a provider needs credentials_file or a wrapped credential"},
		{"operator without a credential", bucket, "a provider needs credentials_file or a wrapped credential"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("Validate() = %v; want nil", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("Validate() = %v; want %q", err, c.want)
			}
		})
	}
}
