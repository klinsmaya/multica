-- Dropping the constraint drops the index it took over.
ALTER TABLE market_install_operation DROP CONSTRAINT IF EXISTS market_install_operation_pkey;
