-- Stage 12.5. Stable line identity across cloned revisions; immutable row-change log.
ALTER TABLE project_work_items ADD COLUMN lineage_id uuid;
UPDATE project_work_items SET lineage_id=id WHERE lineage_id IS NULL;
ALTER TABLE project_work_items ALTER COLUMN lineage_id SET DEFAULT gen_random_uuid();
ALTER TABLE project_work_items ALTER COLUMN lineage_id SET NOT NULL;
CREATE UNIQUE INDEX project_work_items_revision_lineage_idx ON project_work_items(revision_id,lineage_id);
CREATE TABLE work_item_change_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 revision_id uuid NOT NULL REFERENCES project_revisions(id),
 item_id uuid NOT NULL,
 lineage_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('INSERT','UPDATE','DELETE')),
 actor_id uuid REFERENCES users(id),
 happened_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 before_row jsonb,
 after_row jsonb,
 CHECK ((action='INSERT' AND before_row IS NULL AND after_row IS NOT NULL)
 OR (action='DELETE' AND before_row IS NOT NULL AND after_row IS NULL)
 OR (action='UPDATE' AND before_row IS NOT NULL AND after_row IS NOT NULL))
);
CREATE INDEX work_item_change_events_revision_idx ON work_item_change_events(revision_id,id DESC);
CREATE FUNCTION record_work_item_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_actor uuid; v_before jsonb; v_after jsonb;
BEGIN
 IF TG_OP <> 'INSERT' THEN v_before := to_jsonb(OLD); END IF;
 IF TG_OP <> 'DELETE' THEN v_after := to_jsonb(NEW); END IF;
 IF TG_OP = 'UPDATE' AND v_before = v_after THEN RETURN NEW; END IF;
 IF nullif(current_setting('app.actor_id',true),'') IS NOT NULL THEN
   v_actor := current_setting('app.actor_id',true)::uuid;
 END IF;
 INSERT INTO work_item_change_events(revision_id,item_id,lineage_id,action,actor_id,before_row,after_row)
 VALUES(CASE WHEN TG_OP='DELETE' THEN OLD.revision_id ELSE NEW.revision_id END,
        CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END,
        CASE WHEN TG_OP='DELETE' THEN OLD.lineage_id ELSE NEW.lineage_id END,
        TG_OP,v_actor,v_before,v_after);
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;
CREATE TRIGGER project_work_items_audit AFTER INSERT OR UPDATE OR DELETE ON project_work_items
 FOR EACH ROW EXECUTE FUNCTION record_work_item_change();
CREATE FUNCTION forbid_work_item_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'work item change log is immutable'; END $$;
CREATE TRIGGER work_item_change_events_guard BEFORE UPDATE OR DELETE ON work_item_change_events
 FOR EACH ROW EXECUTE FUNCTION forbid_work_item_audit_mutation();

-- Canonicalize referenced item UUIDs into stable lineage IDs before comparison.
CREATE FUNCTION canonical_revision_inputs(p jsonb, rev uuid) RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
DECLARE v jsonb; element jsonb; key text; result jsonb; candidate uuid; stable uuid;
BEGIN
 IF p IS NULL THEN RETURN NULL; END IF;
 IF jsonb_typeof(p) = 'object' THEN
  result := '{}'::jsonb;
  FOR key, element IN SELECT * FROM jsonb_each(p) LOOP
   result := result || jsonb_build_object(key,canonical_revision_inputs(element,rev));
  END LOOP;
  RETURN result;
 ELSIF jsonb_typeof(p) = 'array' THEN
  result := '[]'::jsonb;
  FOR element IN SELECT value FROM jsonb_array_elements(p) LOOP
   result := result || jsonb_build_array(canonical_revision_inputs(element,rev));
  END LOOP;
  RETURN result;
 ELSIF jsonb_typeof(p) = 'string' AND (p #>> '{}') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
  candidate := (p #>> '{}')::uuid;
  SELECT lineage_id INTO stable FROM project_work_items WHERE revision_id=rev AND id=candidate;
  IF stable IS NOT NULL THEN RETURN to_jsonb(stable::text); END IF;
 END IF;
 RETURN p;
END $$;
