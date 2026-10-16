package bootstrap

import (
	"context"
	"time"

	feed "winterflow/internal/domain/catalog"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/catalogfeed"
)

// StartCatalog starts the catalog sync (api, standalone): one goroutine per
// process, never the standalone bridge's, syncing a minute after start and
// then every CATALOG_REFRESH (each sync bounded by 5 minutes). CATALOG_URL
// empty switches the catalog off: every catalog template is withdrawn once
// (hidden from lists and new installs; instances keep working; setting the
// URL again restores them at the next sync). A CATALOG_URL or
// CATALOG_PUBLIC_KEY that can't be used is logged and leaves the sync off:
// the API runs without it.
func StartCatalog(ctx context.Context, d *Deps) {
	startCatalog(ctx, d, catalogsync.Schedule{Every: d.Cfg.GetCatalogRefresh()})
}

// startCatalog is StartCatalog with its schedule (tests); it returns the
// running Syncer, nil when the sync is off.
func startCatalog(ctx context.Context, d *Deps, sch catalogsync.Schedule) *catalogsync.Syncer {
	url := d.Cfg.GetCatalogURL()
	if url == "" {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, catalogsync.DefaultTimeout)
			defer cancel()
			n, err := catalogsync.WithdrawAll(ctx, d.CatalogRepository, time.Now())
			if err != nil {
				d.Log.Error("catalog off: withdrawing the catalog templates failed", "error", err)
				return
			}
			if n > 0 {
				d.Log.Info("catalog off (CATALOG_URL is empty): withdrew the catalog templates", "count", n)
			}
		}()
		return nil
	}
	key, err := feed.ParsePublicKey(d.Cfg.GetCatalogPublicKey())
	if err != nil {
		d.Log.Error("catalog sync off: CATALOG_PUBLIC_KEY", "error", err)
		return nil
	}
	fetcher, err := catalogfeed.New(url)
	if err != nil {
		d.Log.Error("catalog sync off: CATALOG_URL", "error", err)
		return nil
	}
	s := catalogsync.New(catalogsync.Deps{Repo: d.CatalogRepository, Fetcher: fetcher, Key: key, Log: d.Log})
	go s.Run(ctx, sch)
	return s
}
