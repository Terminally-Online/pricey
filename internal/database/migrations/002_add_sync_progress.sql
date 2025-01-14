-- Create table for tracking sync progress
CREATE TABLE IF NOT EXISTS pair_sync_progress (
    pair_address BYTEA PRIMARY KEY REFERENCES pairs(address),
    last_processed_block BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_block CHECK (last_processed_block > 0)
);

-- Create index for quick lookups
CREATE INDEX IF NOT EXISTS idx_sync_progress_block ON pair_sync_progress (last_processed_block);

---- Create above / drop below ----

DROP INDEX IF EXISTS idx_sync_progress_block;
DROP TABLE IF EXISTS pair_sync_progress; 