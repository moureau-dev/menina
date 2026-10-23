package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

func TestStoreAuthorizerScopes(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "authz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateProfile(ctx, identity.Profile{Name: "reader", Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	authz := NewStoreAuthorizer(db)
	actor := Actor{ID: id, Source: "ssh"}

	if err := authz.Authorize(ctx, actor, Request{Method: http.MethodGet, Path: "/status"}); err != nil {
		t.Fatalf("read status: %v", err)
	}
	err = authz.Authorize(ctx, actor, Request{Method: http.MethodPost, Path: "/deploy", Body: json.RawMessage(`{"project":"demo"}`)})
	if err == nil {
		t.Fatal("read profile allowed deploy")
	}
	if code, _, _, _ := apierrors.Info(err); code != apierrors.CodeAuth {
		t.Fatalf("error code = %s", code)
	}
	if err := authz.Authorize(ctx, Actor{ID: "local", Source: "http"}, Request{Method: http.MethodPost, Path: "/deploy"}); err != nil {
		t.Fatalf("local actor rejected: %v", err)
	}

	scopedID, err := db.CreateProfile(ctx, identity.Profile{Name: "deployer", Scopes: []string{"project:demo:deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	scoped := Actor{ID: scopedID, Source: "ssh"}
	if err := authz.Authorize(ctx, scoped, Request{Method: http.MethodPost, Path: "/deploy", Body: json.RawMessage(`{"project":"demo","environment":"production"}`)}); err != nil {
		t.Fatalf("project deploy rejected: %v", err)
	}
	if err := authz.Authorize(ctx, scoped, Request{Method: http.MethodPost, Path: "/deploy", Body: json.RawMessage(`{"project":"other"}`)}); err == nil {
		t.Fatal("project deploy leaked")
	}

	appID, err := db.CreateProfile(ctx, identity.Profile{Name: "app-deployer", Scopes: []string{"project:demo/production/api:deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	appActor := Actor{ID: appID, Source: "ssh"}
	if err := authz.Authorize(ctx, appActor, Request{Method: http.MethodPost, Path: "/deploy", Body: json.RawMessage(`{"project":"demo","environment":"production","app":"api"}`)}); err != nil {
		t.Fatalf("app deploy rejected: %v", err)
	}
	if err := authz.Authorize(ctx, appActor, Request{Method: http.MethodPost, Path: "/deploy", Body: json.RawMessage(`{"project":"demo","environment":"production","app":"web"}`)}); err == nil {
		t.Fatal("app deploy leaked")
	}
}
