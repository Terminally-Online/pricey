-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create enum for token types
DO $$ BEGIN
    CREATE TYPE token_type AS ENUM ('standard', 'stable', 'wrapped_native');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

-- Create check constraint for valid Ethereum addresses (20 bytes)
CREATE OR REPLACE FUNCTION is_valid_eth_address(addr BYTEA) 
RETURNS BOOLEAN AS $$
BEGIN
    RETURN length(addr) = 20;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Create tokens table
CREATE TABLE IF NOT EXISTS tokens (
    address BYTEA PRIMARY KEY,
    symbol VARCHAR(10) NOT NULL,
    decimals INTEGER NOT NULL,
    token_type token_type NOT NULL DEFAULT 'standard',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_token_address CHECK (is_valid_eth_address(address)),
    CONSTRAINT valid_decimals CHECK (decimals >= 0 AND decimals <= 18),
    CONSTRAINT valid_symbol CHECK (length(symbol) > 0)
);

-- Create pairs table
CREATE TABLE IF NOT EXISTS pairs (
    address BYTEA PRIMARY KEY,
    token0_address BYTEA NOT NULL REFERENCES tokens(address),
    token1_address BYTEA NOT NULL REFERENCES tokens(address),
    created_at_block BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_pair_address CHECK (is_valid_eth_address(address)),
    CONSTRAINT different_tokens CHECK (token0_address != token1_address),
    CONSTRAINT valid_block CHECK (created_at_block > 0),
    CONSTRAINT token_order CHECK (token0_address < token1_address) -- Enforce consistent token ordering
);

-- Create hypertable for pair reserves and prices
CREATE TABLE IF NOT EXISTS pair_prices (
    id BIGSERIAL,  -- Add explicit ID for unique constraint
    pair_address BYTEA NOT NULL REFERENCES pairs(address),
    block_number BIGINT NOT NULL,
    block_time TIMESTAMPTZ NOT NULL,
    reserves0 NUMERIC NOT NULL,
    reserves1 NUMERIC NOT NULL,
    price0_usd NUMERIC,
    price1_usd NUMERIC,
    tvl_usd NUMERIC,
    confidence NUMERIC NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_reserves CHECK (reserves0 >= 0 AND reserves1 >= 0),
    CONSTRAINT valid_prices CHECK (
        (price0_usd IS NULL OR price0_usd > 0) AND
        (price1_usd IS NULL OR price1_usd > 0)
    ),
    CONSTRAINT valid_tvl CHECK (tvl_usd IS NULL OR tvl_usd >= 0),
    CONSTRAINT valid_confidence CHECK (confidence >= 0 AND confidence <= 1),
    CONSTRAINT valid_block_number CHECK (block_number > 0),
    CONSTRAINT unique_pair_block_time UNIQUE (pair_address, block_number, block_time)
);

-- Convert to hypertable
SELECT create_hypertable('pair_prices', 'block_time', 
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_pair_prices_pair_block ON pair_prices (pair_address, block_number DESC);
CREATE INDEX IF NOT EXISTS idx_pair_prices_confidence ON pair_prices (confidence) WHERE confidence > 0;
CREATE INDEX IF NOT EXISTS idx_tokens_type ON tokens (token_type) WHERE token_type != 'standard';
CREATE INDEX IF NOT EXISTS idx_pairs_tokens ON pairs (token0_address, token1_address);
CREATE INDEX IF NOT EXISTS idx_pair_prices_time_block ON pair_prices (block_time DESC, block_number DESC);

-- Set up compression if not already configured
DO $$ 
BEGIN
    -- Check if compression is already enabled
    IF NOT EXISTS (
        SELECT 1 
        FROM timescaledb_information.compression_settings 
        WHERE hypertable_name = 'pair_prices'
    ) THEN
        -- Enable compression
        ALTER TABLE pair_prices SET (
            timescaledb.compress,
            timescaledb.compress_segmentby = 'pair_address',
            timescaledb.compress_orderby = 'block_number DESC'
        );

        -- Add compression policy
        SELECT add_compression_policy('pair_prices', INTERVAL '7 days');
    END IF;
END $$;

-- Create function to update timestamps
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Add triggers
DO $$ 
BEGIN
    -- Create trigger for tokens table if it doesn't exist
    IF NOT EXISTS (
        SELECT 1 
        FROM pg_trigger 
        WHERE tgname = 'update_tokens_updated_at'
    ) THEN
        CREATE TRIGGER update_tokens_updated_at
            BEFORE UPDATE ON tokens
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;

    -- Create trigger for pairs table if it doesn't exist
    IF NOT EXISTS (
        SELECT 1 
        FROM pg_trigger 
        WHERE tgname = 'update_pairs_updated_at'
    ) THEN
        CREATE TRIGGER update_pairs_updated_at
            BEFORE UPDATE ON pairs
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

-- Create view for latest prices
CREATE OR REPLACE VIEW latest_pair_prices AS
SELECT DISTINCT ON (pair_address)
    pair_address,
    block_number,
    block_time,
    reserves0,
    reserves1,
    price0_usd,
    price1_usd,
    tvl_usd,
    confidence
FROM pair_prices
ORDER BY pair_address, block_number DESC; 

-- Create pair sync progress table
CREATE TABLE IF NOT EXISTS pair_sync_progress (
    pair_address BYTEA PRIMARY KEY REFERENCES pairs(address),
    last_processed_block BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT valid_block_number CHECK (last_processed_block > 0)
);

-- Add trigger for pair_sync_progress table
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 
        FROM pg_trigger 
        WHERE tgname = 'update_pair_sync_progress_updated_at'
    ) THEN
        CREATE TRIGGER update_pair_sync_progress_updated_at
            BEFORE UPDATE ON pair_sync_progress
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$; 