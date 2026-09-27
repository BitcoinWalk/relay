package main

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestRelayCredentialLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := createRelayCredential(path); err != nil {
		t.Fatal(err)
	}
	first, err := readRelayCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := createRelayCredential(path); err == nil {
		t.Fatal("credential overwritten")
	}
	second, err := readRelayCredential(path)
	if err != nil || first != second {
		t.Fatal("identity changed")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRelayCredential(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRelayCredential(path); err == nil {
		t.Fatal("public-readable key accepted")
	}
	if _, err := readRelayCredential(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted")
	}
}

func credentialACL(uid uint32) []byte {
	data := make([]byte, 44)
	binary.LittleEndian.PutUint32(data, 2)
	for i, tag := range []uint16{1, 2, 4, 16, 32} {
		offset := 4 + 8*i
		binary.LittleEndian.PutUint16(data[offset:], tag)
		id := ^uint32(0)
		if tag == 2 {
			id = uid
		}
		binary.LittleEndian.PutUint32(data[offset+4:], id)
		if tag == 1 || tag == 2 || tag == 16 {
			binary.LittleEndian.PutUint16(data[offset+2:], 4)
		}
	}
	return data
}

func TestSystemdCredentialACL(t *testing.T) {
	uid := uint32(os.Geteuid())
	acl := credentialACL(uid)
	if !serviceOnlyCredentialACL(acl, uid) {
		t.Fatal("service-only ACL rejected")
	}
	for _, offset := range []int{0, 6, 14, 22, 30, 38} {
		bad := append([]byte{}, acl...)
		bad[offset] = 7
		if serviceOnlyCredentialACL(bad, uid) {
			t.Fatalf("unsafe ACL accepted at %d", offset)
		}
	}
	if serviceOnlyCredentialACL(acl, uid+1) || serviceOnlyCredentialACL(acl[:40], uid) {
		t.Fatal("wrong identity or truncated ACL accepted")
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := createRelayCredential(path); err != nil {
		t.Fatal(err)
	}
	original, err := readRelayCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0440 {
		t.Fatalf("expected 0440 ACL-backed file: %v %v", info, err)
	}
	got, err := readRelayCredential(path)
	if err != nil || got != original {
		t.Fatal("ACL-backed credential rejected or changed", err)
	}
	// Real group readability must still fail even with a valid-looking mask.
	unsafe := credentialACL(uid)
	binary.LittleEndian.PutUint16(unsafe[22:], 4)
	if err := unix.Setxattr(path, "system.posix_acl_access", unsafe, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := readRelayCredential(path); err == nil {
		t.Fatal("group-readable credential accepted")
	}
}
