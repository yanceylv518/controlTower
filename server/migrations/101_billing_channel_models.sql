ALTER TABLE billing_upstream_channel_bindings ADD COLUMN models_json JSON NULL;
ALTER TABLE billing_statement_channels ADD COLUMN models_json JSON NULL;
