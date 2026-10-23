// Package contracttest runs the operational state contract against a Store.
package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/release"
	"github.com/moureau-dev/menina/internal/core/state/contract"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// NewStore builds an empty migrated store for one subtest.
type NewStore func(t *testing.T) contract.Store

// Run exercises the shared Store contract.
func Run(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("job lifecycle", func(t *testing.T) {
		store := newStore(t)
		job := sqlite.Job{DedupKey: "job-1", Kind: "manual", Project: "p", Environment: "production", App: "api", Repository: "registry.example.com/p/api", Tag: "production"}
		created, err := store.IngestRegistryEvent(ctx, &sqlite.Event{DedupKey: "event-1", Repository: "p/api", Tag: "production"}, []sqlite.Job{job})
		if err != nil || !created {
			t.Fatalf("ingest created=%v err=%v", created, err)
		}
		pending, err := store.PendingJobs(ctx, 10)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending=%d err=%v", len(pending), err)
		}
		started, err := store.ClaimJob(ctx, pending[0].ID)
		if err != nil || !started {
			t.Fatalf("claim=%v err=%v", started, err)
		}
		if err := store.CompleteJob(ctx, pending[0].ID, "sha256:abc"); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.JobByID(ctx, pending[0].ID)
		if err != nil || !ok || got.Status != sqlite.JobStatusDone {
			t.Fatalf("job=%+v ok=%v err=%v", got, ok, err)
		}
	})

	t.Run("release lifecycle", func(t *testing.T) {
		store := newStore(t)
		id, err := store.CreateRelease(ctx, sqlite.Release{Project: "p", Environment: "production", App: "api", Repository: "registry.example.com/p/api", Tag: "production", Digest: "sha256:abc", Image: "registry.example.com/p/api@sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.TransitionRelease(ctx, id, release.Active, "ready", "test"); err != nil {
			t.Fatal(err)
		}
		rel, ok, err := store.ActiveRelease(ctx, "p", "production", "api")
		if err != nil || !ok || rel.ID != id || rel.State != string(release.Active) {
			t.Fatalf("active=%+v ok=%v err=%v", rel, ok, err)
		}
	})

	t.Run("routes", func(t *testing.T) {
		store := newStore(t)
		want := sqlite.Route{Host: "api.example.com", PathPrefix: "/", Project: "p", Environment: "production", App: "api", Namespace: "p-production", Service: "api", Port: 8080}
		if err := store.ReplaceRoutes(ctx, []sqlite.Route{want}); err != nil {
			t.Fatal(err)
		}
		got, err := store.Routes(ctx)
		if err != nil || len(got) != 1 || got[0].Host != want.Host || got[0].Port != want.Port {
			t.Fatalf("routes=%+v err=%v", got, err)
		}
	})

	t.Run("webhook delivery", func(t *testing.T) {
		store := newStore(t)
		id, err := store.RecordWebhookDelivery(ctx, sqlite.WebhookDelivery{DedupKey: "delivery-1", Project: "p", Environment: "production", App: "api", Event: "deploy.done", Payload: `{"ok":true}`})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteWebhookDelivery(ctx, id, "delivered", 1, 200, ""); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.WebhookDeliveryByID(ctx, id)
		if err != nil || !ok || got.Status != "delivered" || got.ResponseCode != 200 {
			t.Fatalf("delivery=%+v ok=%v err=%v", got, ok, err)
		}
	})

	t.Run("identity profiles and keys", func(t *testing.T) {
		store := newStore(t)
		id, err := store.CreateProfile(ctx, identity.Profile{Name: "operator", Scopes: []string{"deploy", "read"}})
		if err != nil {
			t.Fatal(err)
		}
		profile, ok, err := store.ProfileByID(ctx, id)
		if err != nil || !ok || profile.Name != "operator" || len(profile.Scopes) != 2 {
			t.Fatalf("profile=%+v ok=%v err=%v", profile, ok, err)
		}
		keyID, err := store.AddProfileKey(ctx, identity.Key{ProfileID: id, Fingerprint: "SHA256:test", Comment: "laptop"})
		if err != nil {
			t.Fatal(err)
		}
		keys, err := store.ProfileKeys(ctx, id)
		if err != nil || len(keys) != 1 || keys[0].ID != keyID {
			t.Fatalf("keys=%+v err=%v", keys, err)
		}
		resolved, key, ok, err := store.ResolveKey(ctx, "SHA256:test")
		if err != nil || !ok || resolved.ID != id || key.ID != keyID || resolved.LastSeenAt.IsZero() {
			t.Fatalf("resolved=%+v key=%+v ok=%v err=%v", resolved, key, ok, err)
		}
		if _, _, ok, err := store.ResolveKey(ctx, "SHA256:missing"); err != nil || ok {
			t.Fatalf("unknown key ok=%v err=%v", ok, err)
		}
		profile.Status = identity.StatusDisabled
		if err := store.UpdateProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
		if _, _, ok, err := store.ResolveKey(ctx, "SHA256:test"); err != nil || ok {
			t.Fatalf("disabled profile resolved ok=%v err=%v", ok, err)
		}
	})

	t.Run("enrollment token lifecycle", func(t *testing.T) {
		store := newStore(t)
		profileID, err := store.CreateProfile(ctx, identity.Profile{Name: "new-client", Scopes: []string{"deploy"}})
		if err != nil {
			t.Fatal(err)
		}
		plaintext, token, err := identity.NewEnrollmentToken(profileID, time.Minute)
		if err != nil || plaintext == "" || token.TokenHash == plaintext {
			t.Fatalf("token=%+v plaintext=%q err=%v", token, plaintext, err)
		}
		if _, err := store.CreateEnrollmentToken(ctx, token); err != nil {
			t.Fatal(err)
		}
		profile, key, err := store.ConsumeEnrollmentToken(ctx, token.TokenHash, time.Now(), identity.Key{Fingerprint: "SHA256:enroll"})
		if err != nil || profile.ID != profileID || key.Fingerprint != "SHA256:enroll" {
			t.Fatalf("profile=%+v key=%+v err=%v", profile, key, err)
		}
		if _, _, err := store.ConsumeEnrollmentToken(ctx, token.TokenHash, time.Now(), identity.Key{Fingerprint: "SHA256:again"}); !errors.Is(err, identity.ErrEnrollmentInvalid) {
			t.Fatalf("reused token err=%v", err)
		}
		expiredPlain, expired, err := identity.NewEnrollmentToken(profileID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		expired.TokenHash = identity.HashEnrollmentToken(expiredPlain)
		expired.ExpiresAt = time.Now().Add(-time.Minute)
		if _, err := store.CreateEnrollmentToken(ctx, expired); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ConsumeEnrollmentToken(ctx, expired.TokenHash, time.Now(), identity.Key{Fingerprint: "SHA256:expired"}); !errors.Is(err, identity.ErrEnrollmentInvalid) {
			t.Fatalf("expired token err=%v", err)
		}
	})

	t.Run("observed state", func(t *testing.T) {
		store := newStore(t)
		now := time.Now().UTC().Truncate(time.Second)
		if err := store.UpsertObserved(ctx, sqlite.Observed{Project: "p", Environment: "production", App: "api", Image: "image:tag", Replicas: 2, ReadyReplicas: 2, Status: "ready", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, err := store.Observed(ctx)
		if err != nil || len(got) != 1 || got[0].App != "api" || got[0].ReadyReplicas != 2 {
			t.Fatalf("observed=%+v err=%v", got, err)
		}
		if err := store.DeleteObserved(ctx, "p", "production", "api"); err != nil {
			t.Fatal(err)
		}
		got, err = store.Observed(ctx)
		if err != nil || len(got) != 0 {
			t.Fatalf("observed after delete=%+v err=%v", got, err)
		}
	})
}
