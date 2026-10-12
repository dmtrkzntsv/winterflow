-- Monitoring samples: one row per (server, app or '' for the server itself,
-- tier in minutes, bucket). data is a JSON object {field: avg, field_max: max}
-- (tier 1 holds plain values). Rolled up 1 -> 10 -> 20 -> 120 -> 480 and
-- pruned per tier by the API; bounded by retention, never by row count.

CREATE TABLE metric_samples (
    server_id UUID NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    tier INTEGER NOT NULL,
    bucket_start TIMESTAMP NOT NULL,
    samples INTEGER NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (server_id, app_id, tier, bucket_start),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_metric_samples_tier_bucket ON metric_samples (tier, bucket_start);
