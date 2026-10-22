-- Hub server certificates the API trusts. Agents pin the active set (rows
-- with retired_at NULL): fingerprint is the lowercase hex SHA-256 of the
-- certificate's DER. Trusting a new certificate for a hub id retires that
-- hub id's older ones.
CREATE TABLE IF NOT EXISTS hub_certificates (
    fingerprint TEXT PRIMARY KEY,
    hub_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retired_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_hub_certificates_hub_id ON hub_certificates (hub_id);
