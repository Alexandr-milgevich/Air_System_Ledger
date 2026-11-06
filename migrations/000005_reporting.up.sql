-- Stage 10: independent, append-only cash payment ledger per revision + worker.
-- Never count revisions together: two revisions may contain the same physical work.
CREATE TABLE project_worker_payments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision_id uuid NOT NULL REFERENCES project_revisions(id) ON DELETE RESTRICT,
    worker_id uuid NOT NULL REFERENCES workers(id) ON DELETE RESTRICT,
    amount numeric(20,2) NOT NULL CHECK (amount > 0),
    paid_on date NOT NULL,
    notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 500),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_worker_payments_rev_worker ON project_worker_payments(revision_id,worker_id,paid_on);

CREATE FUNCTION stage10_payment_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'payment ledger is append-only; use a separate reversal workflow';
END;
$$;
CREATE TRIGGER stage10_payment_no_mutation BEFORE UPDATE OR DELETE ON project_worker_payments
 FOR EACH ROW EXECUTE FUNCTION stage10_payment_immutable();

-- Lock worker row for every payment and every approval: protects concurrent money operations.
-- Approval of a negative correction must not reduce accrued wages below already paid sums.
CREATE FUNCTION stage10_worker_accrued(p_revision uuid, p_worker uuid, p_event uuid DEFAULT NULL)
RETURNS numeric LANGUAGE sql STABLE AS $$
 SELECT COALESCE(SUM(ROUND(COALESCE(q.qty,0)*r.rate_per_billable_unit,2)),0)
 FROM work_assignments a JOIN worker_rate_snapshots r ON r.assignment_id=a.id
 LEFT JOIN LATERAL (
   SELECT SUM(e.quantity_delta) AS qty
   FROM work_execution_events e
   WHERE e.assignment_id=a.id AND (
      EXISTS(SELECT 1 FROM work_execution_approvals ap WHERE ap.event_id=e.id)
      OR e.id=p_event
   )
 ) q ON true
 WHERE a.revision_id=p_revision AND a.worker_id=p_worker;
$$;

CREATE FUNCTION stage10_payment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE earned numeric; already_paid numeric;
BEGIN
 PERFORM 1 FROM workers WHERE id=NEW.worker_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'worker missing'; END IF;
 IF NOT EXISTS (
  SELECT 1 FROM project_revisions rev JOIN project_workers pw
   ON pw.project_id=rev.project_id AND pw.worker_id=NEW.worker_id
  WHERE rev.id=NEW.revision_id
 ) THEN
  RAISE EXCEPTION 'worker is not assigned to this project';
 END IF;
 earned := stage10_worker_accrued(NEW.revision_id,NEW.worker_id);
 SELECT COALESCE(SUM(amount),0) INTO already_paid FROM project_worker_payments
   WHERE revision_id=NEW.revision_id AND worker_id=NEW.worker_id;
 IF already_paid+NEW.amount > earned THEN
  RAISE EXCEPTION 'payment exceeds approved unpaid accrual';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage10_payment_insert BEFORE INSERT ON project_worker_payments
 FOR EACH ROW EXECUTE FUNCTION stage10_payment_guard();

CREATE FUNCTION stage10_approval_payment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_worker uuid; v_revision uuid; paid numeric; projected numeric;
BEGIN
 SELECT a.worker_id,a.revision_id INTO v_worker,v_revision
 FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id
 WHERE e.id=NEW.event_id;
 IF v_worker IS NULL THEN RAISE EXCEPTION 'execution event missing'; END IF;
 PERFORM 1 FROM workers WHERE id=v_worker FOR UPDATE;
 projected := stage10_worker_accrued(v_revision,v_worker,NEW.event_id);
 SELECT COALESCE(SUM(amount),0) INTO paid FROM project_worker_payments
 WHERE revision_id=v_revision AND worker_id=v_worker;
 IF projected < paid THEN
  RAISE EXCEPTION 'approval would reduce approved accrual below paid wages';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage10_approval_payment_check BEFORE INSERT ON work_execution_approvals
 FOR EACH ROW EXECUTE FUNCTION stage10_approval_payment_guard();
