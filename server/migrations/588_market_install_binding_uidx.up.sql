CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS market_install_binding_pkey_uidx
    ON market_install_binding (instance_id, kind, resource_id);
