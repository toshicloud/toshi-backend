// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// UserDataHeader is the first line of the EC2 user data a cloud host gives
// its instance. The rest is one JSON object, a UserData.
const UserDataHeader = "#toshi-runner-v1"

// UserData is the runner an EC2 instance runs, in place of the configure
// message the serial path sends. Formatting it with %v redacts the JIT
// configuration.
type UserData struct {
	// JITConfig is the runner's encoded just-in-time configuration.
	JITConfig string `json:"jit_config"`
	// WorkDir is the runner's work folder.
	WorkDir string `json:"work_dir,omitempty"`
	// Env is added to the runner's environment.
	Env map[string]string `json:"env,omitempty"`
	// ShutdownMinutes is when the instance powers off whatever the runner
	// does, counted from when the guest read its user data.
	ShutdownMinutes int `json:"shutdown_minutes"`
	// EgressEchoURLs answer an HTTPS GET with the caller's public address.
	// A NAT host's guest asks them once before the runner starts and
	// reports the address to the control plane.
	EgressEchoURLs []string `json:"egress_echo_urls,omitempty"`
}

// String returns a summary without the JIT configuration.
func (u UserData) String() string {
	return fmt.Sprintf("guestproto.UserData{JITConfig:%s WorkDir:%q Env:%d vars ShutdownMinutes:%d EgressEchoURLs:%d}",
		redactedLen(u.JITConfig), u.WorkDir, len(u.Env), u.ShutdownMinutes, len(u.EgressEchoURLs))
}

// GoString returns the same summary as String.
func (u UserData) GoString() string { return u.String() }

func redactedLen(s string) string {
	if s == "" {
		return "[empty]"
	}
	return fmt.Sprintf("[redacted %d bytes]", len(s))
}

// Encode returns the user data: the header line, then the JSON object.
func (u UserData) Encode() ([]byte, error) {
	if u.JITConfig == "" {
		return nil, errors.New("guestproto: user data needs a JIT configuration")
	}
	if u.ShutdownMinutes <= 0 {
		return nil, errors.New("guestproto: user data needs a shutdown deadline")
	}
	body, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	return append([]byte(UserDataHeader+"\n"), body...), nil
}

// ErrNotToshiUserData means the user data does not start with
// UserDataHeader: the instance was not launched by a cloud host.
var ErrNotToshiUserData = errors.New("guestproto: user data is not a toshi runner")

// DecodeUserData parses what Encode wrote. Errors never quote the data.
func DecodeUserData(b []byte) (UserData, error) {
	header, body, _ := bytes.Cut(b, []byte("\n"))
	if string(bytes.TrimRight(header, "\r")) != UserDataHeader {
		return UserData{}, ErrNotToshiUserData
	}
	var u UserData
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		return UserData{}, errors.New("guestproto: user data is not a valid runner object")
	}
	if u.JITConfig == "" {
		return UserData{}, errors.New("guestproto: user data has no JIT configuration")
	}
	if u.ShutdownMinutes <= 0 {
		return UserData{}, errors.New("guestproto: user data has no shutdown deadline")
	}
	return u, nil
}
