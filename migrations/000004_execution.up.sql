-- Stage 9: immutable approval decisions, verified physical progress and rate snapshots.
-- Apply after 000003_workspace, exactly once.
CREATE TABLE work_execution_approvals (
  event_id uuid PRIMARY KEY REFERENCES work_execution_events(id) ON DELETE RESTRICT,
  approved_by uuid NOT NULL REFERENCES users(id),
  approved_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_execution_approval_user ON work_execution_approvals(approved_by);
CREATE INDEX idx_execution_event_date ON work_execution_events(performed_on);

-- Validate rates and prevent retroactive mutation after a fact has been recorded.
CREATE FUNCTION stage9_rate_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rev_status text; item_unit text; item_project uuid;
BEGIN
 SELECT r.status, i.billable_unit_code, r.project_id INTO rev_status, item_unit, item_project
 FROM work_assignments a JOIN project_work_items i ON i.id=a.work_item_id AND i.revision_id=a.revision_id
 JOIN project_revisions r ON r.id=a.revision_id
 WHERE a.id=NEW.assignment_id FOR UPDATE OF r;
 IF rev_status IS NULL OR rev_status <> 'draft' THEN
   RAISE EXCEPTION 'rates may only be set on a draft revision';
 END IF;
 IF NEW.unit_code <> item_unit OR NEW.currency_code <> 'RUB' THEN
   RAISE EXCEPTION 'worker rate unit/currency mismatch';
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM work_execution_events WHERE assignment_id=NEW.assignment_id) THEN
   RAISE EXCEPTION 'worker rate cannot be edited after any recorded fact';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage9_rate_write BEFORE INSERT OR UPDATE ON worker_rate_snapshots
  FOR EACH ROW EXECUTE FUNCTION stage9_rate_guard();

-- Protect event approvals from changes; revoke by explicit future workflow, not hidden edits.
CREATE FUNCTION stage9_approval_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'approved events cannot be modified';
END;
$$;
CREATE TRIGGER stage9_approval_append_only BEFORE UPDATE OR DELETE ON work_execution_approvals
 FOR EACH ROW EXECUTE FUNCTION stage9_approval_immutable();

-- Serialize all event writes for a work item, including parallel requests from separate users.
CREATE FUNCTION stage9_event_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_total numeric; worker_total numeric; available numeric; item_id uuid;
BEGIN
 SELECT a.work_item_id INTO item_id FROM work_assignments a WHERE a.id=NEW.assignment_id;
 IF item_id IS NULL THEN RAISE EXCEPTION 'assignment missing'; END IF;
 -- This row lock serializes events for all workers on the same line.
 SELECT billable_quantity INTO available FROM project_work_items WHERE id=item_id FOR UPDATE;
 IF NEW.event_type='performed' AND NEW.quantity_delta <= 0 THEN
  RAISE EXCEPTION 'performed quantity must be positive';
 ELSIF NEW.event_type='correction' AND NEW.quantity_delta >= 0 THEN
  RAISE EXCEPTION 'correction quantity must be negative';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM worker_rate_snapshots WHERE assignment_id=NEW.assignment_id) THEN
  RAISE EXCEPTION 'worker rate missing';
 END IF;
 SELECT COALESCE(sum(e.quantity_delta),0) INTO worker_total
 FROM work_execution_events e WHERE e.assignment_id=NEW.assignment_id;
 IF worker_total + NEW.quantity_delta < 0 THEN
  RAISE EXCEPTION 'worker fact cannot be negative';
 END IF;
 SELECT COALESCE(sum(e.quantity_delta),0) INTO current_total
 FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id WHERE a.work_item_id=item_id;
 IF current_total + NEW.quantity_delta > available THEN
  RAISE EXCEPTION 'recorded quantity exceeds the work item billable quantity';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage9_event_insert BEFORE INSERT ON work_execution_events
 FOR EACH ROW EXECUTE FUNCTION stage9_event_guard();

-- Do not shrink or delete work items after any fact has been recorded.
CREATE FUNCTION stage9_item_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fact_total numeric;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM work_assignments a JOIN work_execution_events e ON e.assignment_id=a.id WHERE a.work_item_id=OLD.id) THEN
    RAISE EXCEPTION 'cannot delete a work item with recorded facts';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.billable_unit_code <> OLD.billable_unit_code AND EXISTS (
  SELECT 1 FROM work_assignments a WHERE a.work_item_id=OLD.id
 ) THEN RAISE EXCEPTION 'unit is locked after a worker has been assigned'; END IF;
 SELECT COALESCE(sum(e.quantity_delta),0) INTO fact_total
 FROM work_assignments a JOIN work_execution_events e ON e.assignment_id=a.id
 WHERE a.work_item_id=OLD.id;
 IF NEW.billable_quantity < fact_total THEN
  RAISE EXCEPTION 'cannot reduce the planned quantity below performed quantity';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage9_item_fact_write BEFORE UPDATE OR DELETE ON project_work_items
 FOR EACH ROW EXECUTE FUNCTION stage9_item_fact_guard();

-- Disable assignment/rate changes after recording any execution event.
CREATE FUNCTION stage9_assignment_fact_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM work_execution_events WHERE assignment_id=OLD.id) THEN
   RAISE EXCEPTION 'assignment cannot change after recorded facts';
 END IF;
 RETURN OLD;
END;
$$;
CREATE TRIGGER stage9_assignment_immutable BEFORE UPDATE OR DELETE ON work_assignments
 FOR EACH ROW EXECUTE FUNCTION stage9_assignment_fact_guard();
