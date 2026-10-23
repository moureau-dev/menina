package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/crypto/ssh"
)

// HostKey returns the first SSH host public key under dir matching
// ssh_host_*_key.pub, its authorized_keys line, and its SHA256 fingerprint.
func HostKey(dir string) (string, string, error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "ssh_host_*_key.pub"))
	sort.Strings(paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			continue
		}
		return string(data), ssh.FingerprintSHA256(key), nil
	}
	return "", "", fmt.Errorf("no SSH host public key found in %s", dir)
}
