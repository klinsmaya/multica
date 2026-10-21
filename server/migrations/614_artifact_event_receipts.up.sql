-- Consumer dedupe. A second insert of the same event_id applies nothing.

CREATE TABLE IF NOT EXISTS artifact_event_receipts (
    event_id UUID NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
