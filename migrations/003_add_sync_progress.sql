-- Create table to track global sync progress
CREATE TABLE IF NOT EXISTS global_sync_progress (
    id SERIAL PRIMARY KEY,
    last_processed_block BIGINT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Insert initial record if none exists
INSERT INTO global_sync_progress (id, last_processed_block)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING; 