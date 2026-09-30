-- Keep existing mixed rows intact. Only newly generated bills can be split
-- accurately. Totals alone cannot recover the requests for each discount.
ALTER TABLE billing_compact_daily_totals
  DROP PRIMARY KEY,
  ADD PRIMARY KEY (job_id,bill_day,user_id,token_id,channel_id,model_name,settlement_discount);
