-- Stage 12.8: explicit, audited exceptions for historical XLSX amounts.
-- Never modify historical ledger amounts automatically.
CREATE FUNCTION review_item_snapshot(i project_work_items) RETURNS jsonb
LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
  'name', i.name_snapshot, 'system', i.system_id, 'location', i.location_id,
  'input_quantity', i.input_quantity, 'input_unit', i.input_unit_code,
  'coefficient', i.coefficient, 'calculated', i.calculated_quantity,
  'result_unit', i.result_unit_code, 'billable', i.billable_quantity,
  'billable_unit', i.billable_unit_code, 'unit_price', i.unit_price,
  'amount', i.amount, 'currency', i.currency_code,
  'rule', i.rule_snapshot, 'inputs', i.inputs_snapshot)
$$;

CREATE TABLE review_amount_exceptions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 revision_id uuid NOT NULL REFERENCES project_revisions(id),
 work_item_id uuid NOT NULL,
 calculation_snapshot jsonb NOT NULL,
 reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 15 AND 2000),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','invalidated')),
 accepted_by uuid REFERENCES users(id),
 accepted_at timestamptz,
 invalidated_at timestamptz,
 UNIQUE (revision_id,work_item_id),
 CHECK ((status='accepted') = (accepted_at IS NOT NULL)),
 CHECK ((status='accepted') = (accepted_by IS NOT NULL))
);
CREATE INDEX review_amount_exceptions_revision_idx ON review_amount_exceptions(revision_id,status);

CREATE TABLE review_amount_exception_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 exception_id bigint NOT NULL REFERENCES review_amount_exceptions(id),
 revision_id uuid NOT NULL REFERENCES project_revisions(id),
 work_item_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN('requested','accepted','invalidated','reopened','renewed')),
 actor_id uuid REFERENCES users(id),
 reason text NOT NULL,
 calculation_snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX review_amount_exception_events_revision_idx ON review_amount_exception_events(revision_id,id DESC);
CREATE FUNCTION review_exception_events_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'review exception history is immutable'; END $$;
CREATE TRIGGER review_amount_exception_events_immutable BEFORE UPDATE OR DELETE ON review_amount_exception_events
FOR EACH ROW EXECUTE FUNCTION review_exception_events_guard();

CREATE FUNCTION audit_review_amount_exception() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_action text;
BEGIN
 IF TG_OP='INSERT' THEN v_action:='requested';
 ELSIF NEW.status='accepted' AND OLD.status<>'accepted' THEN v_action:='accepted';
 ELSIF NEW.status='invalidated' AND OLD.status<>'invalidated' THEN v_action:='invalidated';
 ELSIF NEW.status='pending' AND OLD.status='accepted' THEN v_action:='reopened';
 ELSE v_action:='renewed'; END IF;
 INSERT INTO review_amount_exception_events(exception_id,revision_id,work_item_id,action,actor_id,reason,calculation_snapshot)
 VALUES(NEW.id,NEW.revision_id,NEW.work_item_id,v_action,
 CASE WHEN v_action='accepted' THEN NEW.accepted_by
      WHEN v_action='invalidated' THEN NULL
      ELSE NEW.requested_by END,
 NEW.reason,NEW.calculation_snapshot);
 RETURN NEW;
END $$;
CREATE TRIGGER review_amount_exception_audit AFTER INSERT OR UPDATE ON review_amount_exceptions
FOR EACH ROW EXECUTE FUNCTION audit_review_amount_exception();

-- A changed calculation invalidates an existing authorization, in the same transaction.
-- The comparison is specific to billable inputs, rule and amount; mere ordering changes do not invalidate it.
CREATE FUNCTION invalidate_review_amount_exception() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF review_item_snapshot(OLD) IS DISTINCT FROM review_item_snapshot(NEW) THEN
  UPDATE review_amount_exceptions SET status='invalidated', accepted_by=NULL,
   accepted_at=NULL, invalidated_at=clock_timestamp()
  WHERE revision_id=OLD.revision_id AND work_item_id=OLD.id AND status<>'invalidated';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER project_work_items_exception_invalidation AFTER UPDATE ON project_work_items
FOR EACH ROW EXECUTE FUNCTION invalidate_review_amount_exception();

-- Returned reviews require fresh approval of previously accepted exceptions.
CREATE FUNCTION review_exception_return_invalidation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='in_review' AND NEW.status='draft' THEN
  UPDATE review_amount_exceptions SET status='pending',accepted_by=NULL,accepted_at=NULL
  WHERE revision_id=NEW.id AND status='accepted';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER review_exception_return AFTER UPDATE OF status ON project_revisions
FOR EACH ROW EXECUTE FUNCTION review_exception_return_invalidation();
