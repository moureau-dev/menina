package identity

import (
	"fmt"

	"golang.org/x/crypto/ssh"
)

// FingerprintSHA256 returns the standard SHA256 fingerprint for a public key.
func FingerprintSHA256(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// FingerprintAuthorizedKey parses one authorized_keys line and returns its
// fingerprint and comment.
func FingerprintAuthorizedKey(line string) (string, string, error) {
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", "", fmt.Errorf("parse public key: %w", err)
	}
	return ssh.FingerprintSHA256(key), comment, nil
}
