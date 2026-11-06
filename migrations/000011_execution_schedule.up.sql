-- Stage 13.1: operational schedule metadata, independent of immutable financial facts.
CREATE TABLE work_assignment_schedules (
 assignment_id uuid PRIMARY KEY REFERENCES work_assignments(id) ON DELETE RESTRICT,
 planned_start date NOT NULL,
 planned_end date NOT NULL,
 priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','critical')),
 notes text NOT NULL DEFAULT '' CHECK (char_length(notes)<=500),
 updated_by uuid NOT NULL REFERENCES users(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(planned_end >= planned_start)
);
CREATE INDEX idx_work_assignment_schedules_end ON work_assignment_schedules(planned_end,planned_start);
-- Keep scheduling changes attributable and prevent changing the existing ledger facts.
CREATE TABLE work_assignment_schedule_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 assignment_id uuid NOT NULL REFERENCES work_assignments(id) ON DELETE RESTRICT,
 actor_id uuid NOT NULL REFERENCES users(id),
 old_start date, old_end date, old_priority text, old_notes text,
 new_start date NOT NULL, new_end date NOT NULL, new_priority text NOT NULL, new_notes text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_work_assignment_schedule_events_assignment ON work_assignment_schedule_events(assignment_id,created_at DESC);
CREATE FUNCTION stage131_schedule_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
   INSERT INTO work_assignment_schedule_events(assignment_id,actor_id,new_start,new_end,new_priority,new_notes)
   VALUES(NEW.assignment_id,NEW.updated_by,NEW.planned_start,NEW.planned_end,NEW.priority,NEW.notes);
 ELSE
   INSERT INTO work_assignment_schedule_events(assignment_id,actor_id,old_start,old_end,old_priority,old_notes,new_start,new_end,new_priority,new_notes)
   VALUES(NEW.assignment_id,NEW.updated_by,OLD.planned_start,OLD.planned_end,OLD.priority,OLD.notes,
          NEW.planned_start,NEW.planned_end,NEW.priority,NEW.notes);
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER stage131_schedule_changed AFTER INSERT OR UPDATE ON work_assignment_schedules
 FOR EACH ROW EXECUTE FUNCTION stage131_schedule_audit();
CREATE FUNCTION stage131_schedule_events_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'schedule audit is append-only'; END;
$$;
CREATE TRIGGER stage131_schedule_events_guard BEFORE UPDATE OR DELETE ON work_assignment_schedule_events
 FOR EACH ROW EXECUTE FUNCTION stage131_schedule_events_immutable();
