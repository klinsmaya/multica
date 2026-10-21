CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS connector_service_policy_pkey_uidx
    ON connector_service_policy (provider, service);
