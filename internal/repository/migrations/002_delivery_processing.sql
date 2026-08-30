ALTER TABLE webhook_deliveries ADD COLUMN IF NOT EXISTS processed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS webhook_deliveries_processing_idx ON webhook_deliveries(processed_at, received_at);
