-- The catalog feed (sub-project 3). templates.catalog is what the catalog
-- says about a catalog template besides its name and files (the JSON of
-- model.CatalogMeta: summary, description, keywords, tags, alternative_to,
-- license, URLs, logo_url), updated in place by the sync; '' for an org
-- template. templates.catalog_feed_at is the generated_at of the newest
-- feed the sync applied to the template (NULL for an org template and for
-- a catalog row no sync wrote): every sync write is conditional on it, so
-- an older feed (a stale mirror on another replica, or after a restart)
-- never rolls a template back.
ALTER TABLE templates ADD COLUMN catalog TEXT NOT NULL DEFAULT '';
ALTER TABLE templates ADD COLUMN catalog_feed_at TIMESTAMP;
