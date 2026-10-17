-- Attach the CONCURRENTLY-built unique index as the primary key.
ALTER TABLE artifacts
    ADD CONSTRAINT artifacts_pkey PRIMARY KEY USING INDEX artifacts_pkey_uidx;
