package template

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/util"
)

// ListCatalog returns every catalog template's sync state, by slug.
func (r *Repo) ListCatalog(ctx context.Context) ([]model.CatalogState, error) {
	rows, err := r.q.ListCatalogTemplates(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]model.CatalogState, 0, len(rows))
	for _, row := range rows {
		st := model.CatalogState{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Meta: decodeMeta(row.Catalog),
			LatestRevision: row.LatestRevision, Digest: row.Digest,
		}
		if row.WithdrawnAt != nil {
			at := row.WithdrawnAt.Time()
			st.WithdrawnAt = &at
		}
		if row.CatalogFeedAt != nil {
			at := row.CatalogFeedAt.Time()
			st.FeedAt = &at
		}
		out = append(out, st)
	}
	return out, nil
}

// ApplyCatalog inserts, revises or updates one catalog template in one
// transaction; every write is conditional (the slug free, latest_revision
// still base's, no newer feed), and a refused one is model.CatalogSkipped.
func (r *Repo) ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error) {
	meta, err := encodeMeta(&e.Meta)
	if err != nil {
		return "", err
	}
	now := types.NewDateTime()
	feedAt := types.DateTime(e.FeedAt.UTC())
	out := model.CatalogSkipped
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		switch {
		case base == nil:
			id := util.GenerateID()
			n, err := r.q.InsertTemplate(ctx, q, insertParams{
				ID: id, Source: model.TemplateSourceCatalog, Slug: e.Slug, Name: e.Name,
				Catalog: meta, CatalogFeedAt: &feedAt, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogInserted
			return r.insertRevision(ctx, model.TemplateRevision{TemplateID: id, Revision: 1, Body: e.Body, Digest: e.Digest}, now)
		case base.Digest != e.Digest:
			n, err := r.q.BumpCatalogTemplate(ctx, q, catBumpParams{
				Name: e.Name, Catalog: meta, FeedAt: &feedAt, UpdatedAt: now, ID: base.ID, LatestRevision: base.LatestRevision,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogRevised
			return r.insertRevision(ctx, model.TemplateRevision{
				TemplateID: base.ID, Revision: base.LatestRevision + 1, Body: e.Body, Digest: e.Digest,
			}, now)
		default:
			n, err := r.q.UpdateCatalogTemplate(ctx, q, catUpdParams{
				Name: e.Name, Catalog: meta, FeedAt: &feedAt, UpdatedAt: now, ID: base.ID,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogUpdated
			return nil
		}
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// WithdrawCatalog marks a catalog template the feed dropped.
func (r *Repo) WithdrawCatalog(ctx context.Context, id string, at, feedAt time.Time) (bool, error) {
	withdrawn, feed := types.DateTime(at.UTC()), types.DateTime(feedAt.UTC())
	n, err := r.q.WithdrawCatalogTemplate(ctx, r.tx.Querier(ctx), withdrawParams{
		WithdrawnAt: &withdrawn, UpdatedAt: withdrawn, ID: id, FeedAt: &feed,
	})
	return n > 0, err
}

// StampCatalog moves every catalog template's feed forward to feedAt.
func (r *Repo) StampCatalog(ctx context.Context, feedAt time.Time) error {
	at := types.DateTime(feedAt.UTC())
	return r.q.StampCatalogTemplates(ctx, r.tx.Querier(ctx), &at)
}

// encodeMeta is the stored form of a catalog template's metadata ("" for
// none).
func encodeMeta(m *model.CatalogMeta) (string, error) {
	if m == nil {
		return "", nil
	}
	b, err := json.Marshal(m.Normalized())
	return string(b), err
}

// decodeMeta reads stored metadata; "" (a row no sync wrote) or a value
// that does not decode reads as empty, never failing a template read.
func decodeMeta(s string) model.CatalogMeta {
	var m model.CatalogMeta
	_ = json.Unmarshal([]byte(s), &m)
	return m.Normalized()
}
