package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// ParseNotification understands both the CloudEvents format emitted by Zot's
// events extension (v2.1.3+, binary or structured mode) and the OCI
// distribution "events" array for compatibility.
func ParseNotification(headers http.Header, body io.Reader) ([]PushEvent, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	contentType := strings.ToLower(headers.Get("Content-Type"))
	if strings.Contains(contentType, "application/cloudevents+json") {
		return parseStructuredCloudEvent(data)
	}
	if ceType := headers.Get("Ce-Type"); ceType != "" {
		return cloudEventToPush(headers.Get("Ce-Id"), ceType, data)
	}
	return parseOCI(data)
}

type cloudEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type zotImageData struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

func parseStructuredCloudEvent(data []byte) ([]PushEvent, error) {
	var ce cloudEvent
	if err := json.Unmarshal(data, &ce); err != nil {
		return nil, fmt.Errorf("parse cloudevent: %w", err)
	}
	return cloudEventToPush(ce.ID, ce.Type, ce.Data)
}

func cloudEventToPush(id, eventType string, data []byte) ([]PushEvent, error) {
	if !strings.HasSuffix(strings.ToLower(eventType), "image.updated") {
		return nil, nil
	}
	var d zotImageData
	if len(data) > 0 {
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, fmt.Errorf("parse zot image event: %w", err)
		}
	}
	if d.Name == "" {
		return nil, fmt.Errorf("zot image event has no repository name")
	}
	return []PushEvent{{
		DeliveryID: id,
		Repository: d.Name,
		Tag:        d.Reference,
		Digest:     d.Digest,
		Action:     "push",
	}}, nil
}

func parseOCI(data []byte) ([]PushEvent, error) {
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
