-- Results of the commands MCP tools dispatch, so any API replica can answer
-- get_command_result. status is running, succeeded, failed or unknown (the
-- dispatching replica never recorded an outcome); result is the agent's
-- response JSON (at most 1 MiB), error its error text. A running row older
-- than 15 minutes becomes unknown; an ended row (finished_at set) is pruned
-- a day later.
CREATE TABLE IF NOT EXISTS command_results (
    request_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    result TEXT,
    error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_command_results_status_created ON command_results (status, created_at);
CREATE INDEX IF NOT EXISTS idx_command_results_finished_at ON command_results (finished_at);
