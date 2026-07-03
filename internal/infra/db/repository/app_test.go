package repository

import (
	"context"
	"errors"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func newAppRepo(t *testing.T) *DbAppRepository {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := newTestDB(t)
	// Apps reference a server row; seed one.
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	return NewDbAppRepository(conn, log)
}

func TestAppCRUDRoundTrip(t *testing.T) {
	repo := newAppRepo(t)
	ctx := context.Background()

	app := model.App{ID: "app-1", ServerID: "srv-1", Name: "grafana", Version: "1", Icon: "chart", Color: "#123456"}
	if err := repo.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetApp(ctx, "app-1")
	if err != nil || got.Name != "grafana" || got.ServerID != "srv-1" {
		t.Fatalf("GetApp = %+v, %v", got, err)
	}

	// Upsert on conflict: same id, new name.
	app.Name = "grafana-2"
	if err := repo.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetApp(ctx, "app-1")
	if got.Name != "grafana-2" {
		t.Fatalf("upsert name = %q", got.Name)
	}

	if err := repo.RenameApp(ctx, "app-1", "renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetApp(ctx, "app-1")
	if got.Name != "renamed" {
		t.Fatalf("renamed = %q", got.Name)
	}

	list, err := repo.GetApps(ctx, "srv-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("GetApps = %+v, %v", list, err)
	}

	if err := repo.DeleteApp(ctx, "app-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetApp(ctx, "app-1"); !errors.Is(err, model.ErrAppNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}

func TestSyncAppsMirrorsAgentState(t *testing.T) {
	repo := newAppRepo(t)
	ctx := context.Background()

	seed := []model.App{
		{ID: "keep", ServerID: "srv-1", Name: "keep"},
		{ID: "drop", ServerID: "srv-1", Name: "drop"},
	}
	for _, a := range seed {
		if err := repo.CreateApp(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	// Agent reports: keep (renamed) + a new app; "drop" is gone.
	err := repo.SyncApps(ctx, "srv-1", []model.App{
		{ID: "keep", Name: "keep-updated"},
		{ID: "new", Name: "brand-new"},
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := repo.GetApps(ctx, "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, a := range list {
		names[a.ID] = a.Name
	}
	if len(list) != 2 || names["keep"] != "keep-updated" || names["new"] != "brand-new" {
		t.Fatalf("after sync: %v", names)
	}
}

func TestSyncAppsEmptyReportDeletesAll(t *testing.T) {
	repo := newAppRepo(t)
	ctx := context.Background()
	if err := repo.CreateApp(ctx, model.App{ID: "a", ServerID: "srv-1", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SyncApps(ctx, "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.GetApps(ctx, "srv-1")
	if len(list) != 0 {
		t.Fatalf("want empty after empty sync, got %v", list)
	}
}
