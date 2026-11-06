-- Returning to the Stage 1 semantics for the content guard.
CREATE OR REPLACE FUNCTION guard_revision_content() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous_status text; next_status text;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  SELECT status INTO previous_status FROM project_revisions WHERE id=OLD.revision_id FOR UPDATE;
  IF previous_status IS NULL OR previous_status IN ('approved','archived') THEN
   RAISE EXCEPTION 'previous revision is immutable or missing';
  END IF;
 END IF;
 IF TG_OP <> 'DELETE' THEN
  SELECT status INTO next_status FROM project_revisions WHERE id=NEW.revision_id FOR UPDATE;
  IF next_status IS NULL OR next_status IN ('approved','archived') THEN
   RAISE EXCEPTION 'next revision is immutable or missing';
  END IF;
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;
DROP TRIGGER revision_review_events_guard ON revision_review_events;
DROP FUNCTION revision_review_events_immutable();
DROP TRIGGER revision_review_audit ON project_revisions;
DROP FUNCTION revision_review_audit_trigger();
DROP TABLE revision_review_events;
