package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// TestDynamicProfilePatch verifies that patching the measured sync_user_profile(49)
// and get_users(48) templates swaps in a fresh Nextendo identity (pid + name + code)
// and reframes the struct so it re-parses byte-for-byte: right pid, right name, and a
// top-level length that matches the body (a wrong length desyncs SMM2's parser).
func TestDynamicProfilePatch(t *testing.T) {
	s := nex.NewSwitchSettings(accessKey, nexVersion)
	const testPID = 1001
	const testName = "Tester"

	// sync_user_profile(49): [pid u64][name string][rest] — the OWN profile.
	m49, err := os.ReadFile("measured/resp_0x73_m49.bin")
	if err != nil {
		t.Fatalf("read m49: %v", err)
	}
	p49 := patchSyncProfile(s, m49, testPID, testName)
	assertFramed(t, "m49", p49)
	in := nex.NewStreamIn(p49[5:], s)
	if got := in.PID(); got != testPID {
		t.Errorf("m49 pid = %d, want %d", got, testPID)
	}
	if got := in.String(); got != testName {
		t.Errorf("m49 name = %q, want %q", got, testName)
	}

	// get_users(48): carve a UserInfo shell, patch [pid u64][code string][name string].
	m48, err := os.ReadFile("measured/resp_0x73_m48.bin")
	if err != nil {
		t.Fatalf("read m48: %v", err)
	}
	carveUserTemplates(m48)
	if len(userInfoTemplate) == 0 || len(resultSuccessTpl) != 4 {
		t.Fatalf("carve failed: userTmpl=%d resultTpl=%d", len(userInfoTemplate), len(resultSuccessTpl))
	}
	pu := patchUserInfo(s, userInfoTemplate, testPID, testName)
	assertFramed(t, "user", pu)
	in = nex.NewStreamIn(pu[5:], s)
	if got := in.PID(); got != testPID {
		t.Errorf("user pid = %d, want %d", got, testPID)
	}
	code := in.String()
	if got := in.String(); got != testName {
		t.Errorf("user name = %q, want %q", got, testName)
	}
	if code != makerCode(testPID) {
		t.Errorf("maker code = %q, want derived %q", code, makerCode(testPID))
	}
}

// assertFramed checks the [version u8][length u32][body] header length matches the body.
func assertFramed(t *testing.T, tag string, b []byte) {
	t.Helper()
	if len(b) < 5 {
		t.Fatalf("%s too short: %d", tag, len(b))
	}
	if l := binary.LittleEndian.Uint32(b[1:5]); int(l) != len(b)-5 {
		t.Errorf("%s header len = %d, body = %d", tag, l, len(b)-5)
	}
}

func TestUploadHostRewrite(t *testing.T) {
	if len(ourUploadHost()) != len(s3UploadHost) {
		t.Fatalf("ourUploadHost=%d must equal s3UploadHost=%d (same-length swap)", len(ourUploadHost()), len(s3UploadHost))
	}
	for _, meth := range []int{66, 132} {
		b, err := os.ReadFile("measured/resp_0x73_m" + strconv.Itoa(meth) + ".bin")
		if err != nil {
			t.Fatalf("read m%d: %v", meth, err)
		}
		n := bytes.Count(b, s3UploadHost)
		if n == 0 {
			t.Errorf("m%d: S3 host string not found", meth)
		}
		rw := rewriteUploadHost(b)
		if len(rw) != len(b) {
			t.Errorf("m%d: rewrite changed size %d->%d", meth, len(b), len(rw))
		}
		if bytes.Count(rw, s3UploadHost) != 0 {
			t.Errorf("m%d: S3 host still present after rewrite", meth)
		}
		if bytes.Count(rw, ourUploadHost()) != n {
			t.Errorf("m%d: our host count=%d want %d", meth, bytes.Count(rw, ourUploadHost()), n)
		}
		t.Logf("m%d: %d S3-host occurrence(s) rewritten, size stable %d", meth, n, len(b))
	}
}
