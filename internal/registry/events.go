package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Notification struct {
	Events []RawEvent `json:"events"`
}

type RawEvent struct {
	ID        string  `json:"id"`
	Timestamp string  `json:"timestamp"`
	Action    string  `json:"action"`
	Target    Target  `json:"target"`
	Request   Request `json:"request"`
}

type Target struct {
	MediaType  string `json:"mediaType"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
	Tag        string `json:"tag"`
}

type Request struct {
	ID     string `json:"id"`
	Addr   string `json:"addr"`
	Host   string `json:"host"`
	Method string `json:"method"`
}

type PushEvent struct {
	DeliveryID string
	Registry   string
	Repository string
	Tag        string
	Digest     string
	Action     string
}

func ParseNotification(body io.Reader) ([]PushEvent, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var n Notification
	if err := json.Unmarshal(data, &n); err == nil && len(n.Events) > 0 {
		return normalize(n.Events), nil
	}
	var single RawEvent
	if err := json.Unmarshal(data, &single); err == nil && (single.Target.Repository != "" || single.Target.Digest != "") {
		return normalize([]RawEvent{single}), nil
	}
	return nil, fmt.Errorf("unrecognized registry notification payload")
}

func normalize(events []RawEvent) []PushEvent {
	out := make([]PushEvent, 0, len(events))
	for _, e := range events {
		delivery := e.ID
		if delivery == "" {
			delivery = e.Request.ID
		}
		out = append(out, PushEvent{
			DeliveryID: delivery,
			Repository: e.Target.Repository,
			Tag:        e.Target.Tag,
			Digest:     e.Target.Digest,
			Action:     e.Action,
		})
	}
	return out
}

func DedupKey(registry, repository, digest, delivery string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", registry, repository, digest, delivery)
	return hex.EncodeToString(h.Sum(nil))
}

func (e PushEvent) IsPush() bool {
	a := strings.ToLower(e.Action)
	return a == "" || a == "push"
}

func (e PushEvent) RepositoryPath() string {
	return strings.TrimPrefix(e.Repository, "/")
}
