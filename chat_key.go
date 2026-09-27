package main

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"fiatjaf.com/nostr"
	"golang.org/x/sys/unix"
)

func readRelayCredential(path string) (nostr.SecretKey, error) {
	var empty nostr.SecretKey
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return empty, errors.New("cannot open relay credential")
	}
	f := os.NewFile(uintptr(fd), "relay-credential")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 64 || info.Size() > 65 {
		return empty, errors.New("relay credential must be a private regular file containing a hex key")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
		return empty, errors.New("relay credential has an untrusted owner")
	}
	if info.Mode().Perm()&0077 != 0 {
		// POSIX ACL masks occupy the group permission bits. systemd's 0440
		// credential may grant ONLY the service UID read access, not a group.
		acl := make([]byte, 4096)
		n, err := unix.Fgetxattr(fd, "system.posix_acl_access", acl)
		if info.Mode().Perm() != 0440 || err != nil || !serviceOnlyCredentialACL(acl[:n], uint32(os.Geteuid())) {
			return empty, errors.New("relay credential permissions allow access beyond the service user")
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return empty, errors.New("cannot read relay credential")
	}
	key, err := nostr.SecretKeyFromHex(strings.TrimSpace(string(data)))
	if err != nil || key == empty {
		return empty, errors.New("invalid relay credential")
	}
	return key, nil
}

// Accept the narrow Linux ACL shape used for systemd LoadCredential: owner
// read, service-UID read, group none, mask read, others none. Unknown entries,
// extra identities, write permissions and malformed ACLs fail closed.
func serviceOnlyCredentialACL(data []byte, uid uint32) bool {
	if len(data) != 44 || binary.LittleEndian.Uint32(data[:4]) != 2 {
		return false
	}
	seen := map[uint16]bool{}
	for offset := 4; offset < len(data); offset += 8 {
		tag := binary.LittleEndian.Uint16(data[offset:])
		perm := binary.LittleEndian.Uint16(data[offset+2:])
		id := binary.LittleEndian.Uint32(data[offset+4:])
		if seen[tag] {
			return false
		}
		seen[tag] = true
		switch tag {
		case 1, 16:
			if perm != 4 || id != ^uint32(0) {
				return false
			}
		case 2:
			if perm != 4 || id != uid {
				return false
			}
		case 4, 32:
			if perm != 0 || id != ^uint32(0) {
				return false
			}
		default:
			return false
		}
	}
	return seen[1] && seen[2] && seen[4] && seen[16] && seen[32]
}

func createRelayCredential(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create new relay credential; existing files are never overwritten")
	}
	key := nostr.Generate()
	_, err = f.WriteString(key.Hex() + "\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return errors.New("credential write failed; check incomplete file before retrying")
	}
	return closeErr
}
