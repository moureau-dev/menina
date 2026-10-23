package sqlite

import (
	"context"
	"testing"
)

func TestWebhookDeliveryLifecycle(t *testing.T) {
	db := testJobsDB(t)
	ctx := context.Background()
	id, err := db.RecordWebhookDelivery(ctx, WebhookDelivery{DedupKey: "d1", Project: "p", Environment: "prod", App: "api", Event: "deploy.succeeded", Payload: `{"event":"deploy.succeeded"}`})
	if err != nil {
		t.Fatal(err)
	}
	again, err := db.RecordWebhookDelivery(ctx, WebhookDelivery{DedupKey: "d1", Project: "p", Environment: "prod", App: "api", Event: "deploy.succeeded"})
	if err != nil || again != id {
		t.Fatalf("duplicate delivery must be idempotent: id=%s again=%s err=%v", id, again, err)
	}
	if err := db.CompleteWebhookDelivery(ctx, id, "failed", 3, 500, "receiver returned 500"); err != nil {
		t.Fatal(err)
	}
	list, err := db.WebhookDeliveries(ctx, "failed", 10)
	if err != nil || len(list) != 1 || list[0].Attempts != 3 || list[0].ResponseCode != 500 {
		t.Fatalf("deliveries = %+v err=%v", list, err)
	}
}
