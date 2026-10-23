package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// HostKey returns the first SSH host public key under dir matching
// ssh_host_*_key.pub, its authorized_keys line, and its SHA256 fingerprint.
func HostKey(dir string) (string, string, error) {
	patterns := []string{"ssh_host_ed25519_key.pub", "ssh_host_ecdsa_key.pub", "ssh_host_rsa_key.pub", "ssh_host_dsa_key.pub", "ssh_host_*_key.pub"}
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			key, _, _, _, err := ssh.ParseAuthorizedKey(data)
			if err != nil {
				continue
			}
			return strings.TrimSpace(string(data)), ssh.FingerprintSHA256(key), nil
		}
	}
	return "", "", fmt.Errorf("no SSH host public key found in %s", dir)
}
