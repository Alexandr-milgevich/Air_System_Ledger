DROP TRIGGER IF EXISTS trg_catalog_audit_immutable ON work_catalog_events;
DROP FUNCTION IF EXISTS prevent_catalog_event_changes();
DROP TABLE IF EXISTS work_catalog_events;
DROP INDEX IF EXISTS idx_work_catalog_active_category;
ALTER TABLE work_catalog
    DROP CONSTRAINT IF EXISTS work_catalog_dims_positive,
    DROP CONSTRAINT IF EXISTS work_catalog_coefficient_nonnegative,
    DROP CONSTRAINT IF EXISTS work_catalog_price_nonnegative;
ALTER TABLE work_catalog
    DROP COLUMN IF EXISTS coefficient,
    DROP COLUMN IF EXISTS unit_price,
    DROP COLUMN IF EXISTS width_mm,
    DROP COLUMN IF EXISTS height_mm,
    DROP COLUMN IF EXISTS diameter_mm,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS updated_at;
