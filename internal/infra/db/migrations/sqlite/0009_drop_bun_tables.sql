-- Bun's bookkeeping is superseded by schema_migrations (adopted from it on
-- first boot of this build). A Bun build can no longer run on this database.

DROP TABLE IF EXISTS bun_migrations;
DROP TABLE IF EXISTS bun_migration_locks;
