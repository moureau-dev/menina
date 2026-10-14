package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Event is the generic, provider-agnostic deploy notification payload. The
// receiver is responsible for fanning it out to Slack/Discord/Sentry/etc.
type Event struct {
	Event       string
	Action      string
	Project     string
	Environment string
	App         string
	Repository  string
	Tag         string
	Digest      string
	Image       string
	Status      string
	Actor       string
	Message     string
	Timestamp   time.Time
}

type Webhook struct {
	URL    string
	Secret string
	Events []string
}

func (w Webhook) Wants(event string) bool {
	if len(w.Events) == 0 {
		return true
	}
	for _, e := range w.Events {
		if e == event {
			return true
		}
	}
	return false
}

type Sender struct {
	log      *slog.Logger
	client   *http.Client
	attempts int
	backoff  time.Duration
}

func NewSender(log *slog.Logger) *Sender {
	return &Sender{
		log: log,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		attempts: 3,
		backoff:  1 * time.Second,
	}
}

// Send delivers the event best-effort: one provider-agnostic JSON POST with a
// short timeout and a few retries. It never blocks the caller's deployment.
func (s *Sender) Send(ctx context.Context, w Webhook, ev Event) error {
	if w.URL == "" || !w.Wants(ev.Event) {
		return nil
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	body, err := render(ev)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < s.attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * s.backoff):
			}
		}
		if lastErr = s.post(ctx, w, ev, body); lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("webhook delivery failed after %d attempts: %w", s.attempts, lastErr)
}

func (s *Sender) post(ctx context.Context, w Webhook, ev Event, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Menina-Event", ev.Event)
	req.Header.Set("X-Menina-Delivery", deliveryID())
	if w.Secret != "" {
		req.Header.Set("X-Menina-Signature", Signature(body, w.Secret))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("receiver returned %s", resp.Status)
	}
	return nil
}

// Signature returns the HMAC-SHA256 header value for a payload so a receiver can
// verify authenticity.
func Signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type payload struct {
	Event       string `json:"event"`
	Action      string `json:"action"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	App         string `json:"app"`
	Repository  string `json:"repository"`
	Tag         string `json:"tag"`
	Digest      string `json:"digest"`
	Image       string `json:"image"`
	Status      string `json:"status"`
	Actor       string `json:"actor"`
	Message     string `json:"message"`
	Timestamp   string `json:"timestamp"`
}

func render(ev Event) ([]byte, error) {
	return json.Marshal(payload{
		Event:       ev.Event,
		Action:      ev.Action,
		Project:     ev.Project,
		Environment: ev.Environment,
		App:         ev.App,
		Repository:  ev.Repository,
		Tag:         ev.Tag,
		Digest:      ev.Digest,
		Image:       ev.Image,
		Status:      ev.Status,
		Actor:       ev.Actor,
		Message:     ev.Message,
		Timestamp:   ev.Timestamp.UTC().Format(time.RFC3339),
	})
}

func deliveryID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
