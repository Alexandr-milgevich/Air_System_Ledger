-- Stage 14: reusable catalog of installation work (historical item snapshots remain immutable).
ALTER TABLE work_catalog
    ADD COLUMN IF NOT EXISTS coefficient numeric(20, 10),
    ADD COLUMN IF NOT EXISTS unit_price  numeric(20, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS width_mm    numeric(12, 3),
    ADD COLUMN IF NOT EXISTS height_mm   numeric(12, 3),
    ADD COLUMN IF NOT EXISTS diameter_mm numeric(12, 3),
    ADD COLUMN IF NOT EXISTS created_by  uuid REFERENCES users (id),
    ADD COLUMN IF NOT EXISTS updated_at  timestamptz    NOT NULL DEFAULT now();
ALTER TABLE work_catalog
    ADD CONSTRAINT work_catalog_price_nonnegative CHECK (unit_price >= 0),
    ADD CONSTRAINT work_catalog_coefficient_nonnegative CHECK (coefficient IS NULL OR coefficient >= 0),
    ADD CONSTRAINT work_catalog_dims_positive CHECK ((width_mm IS NULL OR width_mm > 0) AND
                                                     (height_mm IS NULL OR height_mm > 0) AND
                                                     (diameter_mm IS NULL OR diameter_mm > 0));
CREATE INDEX idx_work_catalog_active_category ON work_catalog (category_code, name) WHERE active;
CREATE TABLE work_catalog_events
(
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    catalog_id   uuid        NOT NULL REFERENCES work_catalog (id) ON DELETE RESTRICT,
    actor_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    action       text        NOT NULL CHECK (action IN ('create', 'update', 'deactivate', 'activate')),
    before_state jsonb,
    after_state  jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE OR REPLACE FUNCTION prevent_catalog_event_changes() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'work catalog audit is append-only';
END;
$$;
CREATE TRIGGER trg_catalog_audit_immutable
    BEFORE UPDATE OR DELETE
    ON work_catalog_events
    FOR EACH ROW
EXECUTE FUNCTION prevent_catalog_event_changes();
