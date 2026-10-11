-- app_domains.app_id/server_id were CHAR(36) while apps.app_id and
-- servers.server_id are UUID, so the claim join failed with "operator does
-- not exist: uuid = character". app_domains is a rebuildable index, so rows
-- whose ids are not UUIDs are deleted first: one such row would make the
-- cast fail and crashloop startup.

DELETE FROM app_domains
WHERE app_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR server_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

ALTER TABLE app_domains
    ALTER COLUMN app_id TYPE UUID USING app_id::uuid,
    ALTER COLUMN server_id TYPE UUID USING server_id::uuid;
