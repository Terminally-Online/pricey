-- Update token fields to use TEXT
ALTER TABLE tokens ALTER COLUMN name TYPE TEXT;
ALTER TABLE tokens ALTER COLUMN symbol TYPE TEXT;

---- Create above / drop below ----

ALTER TABLE tokens ALTER COLUMN name TYPE VARCHAR(100);
ALTER TABLE tokens ALTER COLUMN symbol TYPE VARCHAR(32); 