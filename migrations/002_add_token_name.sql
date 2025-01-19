-- Add name column to tokens table
ALTER TABLE tokens ADD COLUMN name VARCHAR(100);

-- Update existing tokens to use symbol as name if name is null
UPDATE tokens SET name = symbol WHERE name IS NULL; 