package secrets

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	SchemeEnv     = "env"
	SchemeFile    = "file"
	SchemeLiteral = "literal"
	SchemeRandom  = "random"
)

const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GeneratePassword(length int) (string, error) {
	if length <= 0 {
		length = 32
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = passwordAlphabet[int(buf[i])%len(passwordAlphabet)]
	}
	return string(buf), nil
}

type Resolver struct {
	env map[string]string
}

func NewResolver() *Resolver {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return &Resolver{env: env}
}

func (r *Resolver) Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	scheme, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return ref, nil
	}
	switch scheme {
	case SchemeEnv:
		v, ok := r.env[rest]
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", rest)
		}
		return v, nil
	case SchemeFile:
		data, err := os.ReadFile(rest)
		if err != nil {
			return "", fmt.Errorf("read secret file %q: %w", rest, err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	case SchemeLiteral:
		return rest, nil
	default:
		return "", fmt.Errorf("unsupported secret reference scheme %q", scheme)
	}
}

func Redact(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}

func WriteSecretFile(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	// Explicit chmod so the mode is not masked by the process umask, which
	// would otherwise break group access (e.g. umask 022 -> 0640/0750).
	if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(value), 0o660); err != nil {
		return err
	}
	return os.Chmod(path, 0o660)
}

func ReadSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}
