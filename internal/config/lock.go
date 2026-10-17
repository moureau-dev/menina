package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const lockFileName = ".apply.lock"
const lockStaleAfter = 60 * time.Second

type lockInfo struct {
	Token   string    `json:"token"`
	PID     int       `json:"pid"`
	User    string    `json:"user"`
	Host    string    `json:"host"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Lock is a config-dir apply lock with a heartbeat. It uses atomic create
// (O_EXCL) rather than relying on mtime alone; stale locks are recovered after
// a minute without a heartbeat.
type Lock struct {
	path  string
	token string
	stop  chan struct{}
	done  chan struct{}
}

func AcquireLock(configDir string, wait time.Duration) (*Lock, error) {
	path := filepath.Join(configDir, lockFileName)
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err == nil {
			info := lockInfo{
				Token: token, PID: os.Getpid(), User: currentUser(), Host: currentHost(),
				Created: time.Now().UTC(), Updated: time.Now().UTC(),
			}
			data, _ := json.Marshal(info)
			_, _ = f.Write(data)
			_ = f.Close()
			l := &Lock{path: path, token: token, stop: make(chan struct{}), done: make(chan struct{})}
			go l.heartbeat()
			return l, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if stale, info := lockIsStale(path); stale {
			_ = os.Remove(path)
			_ = info
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("configuration is locked by another apply (%s); retry or remove %s if it is stale", lockOwner(path), path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *Lock) Release() {
	if l == nil {
		return
	}
	close(l.stop)
	<-l.done
	data, err := os.ReadFile(l.path)
	if err == nil {
		var info lockInfo
		if json.Unmarshal(data, &info) == nil && info.Token == l.token {
			_ = os.Remove(l.path)
		}
	}
}

func (l *Lock) heartbeat() {
	defer close(l.done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			info := lockInfo{
				Token: l.token, PID: os.Getpid(), User: currentUser(), Host: currentHost(),
				Created: time.Now().UTC(), Updated: time.Now().UTC(),
			}
			data, _ := json.Marshal(info)
			_ = os.WriteFile(l.path, data, 0o640)
		}
	}
}

func lockIsStale(path string) (bool, lockInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, lockInfo{}
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > lockStaleAfter {
			return true, info
		}
		return false, info
	}
	updated := info.Updated
	if updated.IsZero() {
		if st, err := os.Stat(path); err == nil {
			updated = st.ModTime()
		}
	}
	return time.Since(updated) > lockStaleAfter, info
}

func lockOwner(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown owner"
	}
	var info lockInfo
	if json.Unmarshal(data, &info) != nil {
		return "unknown owner"
	}
	return fmt.Sprintf("%s@%s pid %d since %s", info.User, info.Host, info.PID, info.Created.Format(time.RFC3339))
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}

func currentHost() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
