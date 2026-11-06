DROP FUNCTION IF EXISTS canonical_revision_inputs(jsonb,uuid);
DROP TRIGGER IF EXISTS project_work_items_audit ON project_work_items;
DROP FUNCTION IF EXISTS record_work_item_change();
DROP TRIGGER IF EXISTS work_item_change_events_guard ON work_item_change_events;
DROP FUNCTION IF EXISTS forbid_work_item_audit_mutation();
DROP TABLE IF EXISTS work_item_change_events;
DROP INDEX IF EXISTS project_work_items_revision_lineage_idx;
ALTER TABLE project_work_items DROP COLUMN IF EXISTS lineage_id;
