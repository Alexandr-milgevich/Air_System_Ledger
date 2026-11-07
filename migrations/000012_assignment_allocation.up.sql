-- Stage 13.2: operational allocation, independent of actual execution and payment rates.
CREATE TABLE work_assignment_allocations
(
    assignment_id    uuid PRIMARY KEY REFERENCES work_assignments (id) ON DELETE RESTRICT,
    planned_quantity numeric(20, 8) NOT NULL CHECK (planned_quantity >= 0),
    updated_by       uuid           NOT NULL REFERENCES users (id),
    updated_at       timestamptz    NOT NULL DEFAULT now()
);
CREATE TABLE work_assignment_allocation_events
(
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    assignment_id uuid           NOT NULL REFERENCES work_assignments (id) ON DELETE RESTRICT,
    actor_id      uuid           NOT NULL REFERENCES users (id),
    old_quantity  numeric(20, 8),
    new_quantity  numeric(20, 8) NOT NULL,
    created_at    timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX idx_assignment_allocation_events ON work_assignment_allocation_events (assignment_id, id DESC);

-- Serialize competing allocations on the parent work item to avoid overbooking.
CREATE FUNCTION stage132_validate_allocation() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    item_id         uuid;
    max_quantity    numeric;
    allocated_other numeric;
    state           text;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.assignment_id <> OLD.assignment_id THEN
        RAISE EXCEPTION 'cannot reassign allocation';
    END IF;
    SELECT i.id, i.billable_quantity, r.status
    INTO item_id,max_quantity,state
    FROM work_assignments a
             JOIN project_work_items i ON i.id = a.work_item_id AND i.revision_id = a.revision_id
             JOIN project_revisions r ON r.id = a.revision_id
    WHERE a.id = NEW.assignment_id
        FOR UPDATE OF i;
    IF NOT FOUND THEN RAISE EXCEPTION 'assignment not found'; END IF;
    IF state = 'archived' THEN RAISE EXCEPTION 'archived revision cannot be scheduled'; END IF;
    SELECT COALESCE(SUM(p.planned_quantity), 0)
    INTO allocated_other
    FROM work_assignment_allocations p
             JOIN work_assignments a ON a.id = p.assignment_id
    WHERE a.work_item_id = item_id
      AND a.id <> NEW.assignment_id;
    IF allocated_other + NEW.planned_quantity > max_quantity THEN
        RAISE EXCEPTION 'planned allocation exceeds work item quantity';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stage132_allocation_guard
    BEFORE INSERT OR UPDATE
    ON work_assignment_allocations
    FOR EACH ROW
EXECUTE FUNCTION stage132_validate_allocation();
CREATE FUNCTION stage132_allocation_audit() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO work_assignment_allocation_events(assignment_id, actor_id, new_quantity)
        VALUES (NEW.assignment_id, NEW.updated_by, NEW.planned_quantity);
    ELSIF OLD.planned_quantity IS DISTINCT FROM NEW.planned_quantity THEN
        INSERT INTO work_assignment_allocation_events(assignment_id, actor_id, old_quantity, new_quantity)
        VALUES (NEW.assignment_id, NEW.updated_by, OLD.planned_quantity, NEW.planned_quantity);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stage132_allocation_changed
    AFTER INSERT OR UPDATE
    ON work_assignment_allocations
    FOR EACH ROW
EXECUTE FUNCTION stage132_allocation_audit();
CREATE FUNCTION stage132_allocation_events_immutable() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'allocation audit is append-only';
END;
$$;
CREATE TRIGGER stage132_allocation_events_guard
    BEFORE UPDATE OR DELETE
    ON work_assignment_allocation_events
    FOR EACH ROW
EXECUTE FUNCTION stage132_allocation_events_immutable();

-- A later edit to a draft line must not undercut already distributed plans.
CREATE FUNCTION stage132_item_allocation_guard() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    allocated numeric;
BEGIN
    IF NEW.billable_quantity IS DISTINCT FROM OLD.billable_quantity THEN
        SELECT COALESCE(SUM(p.planned_quantity), 0)
        INTO allocated
        FROM work_assignments a
                 JOIN work_assignment_allocations p ON p.assignment_id = a.id
        WHERE a.work_item_id = OLD.id;
        IF NEW.billable_quantity < allocated THEN
            RAISE EXCEPTION 'work item quantity below distributed allocation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER stage132_item_allocation_guard
    BEFORE UPDATE OF billable_quantity
    ON project_work_items
    FOR EACH ROW
EXECUTE FUNCTION stage132_item_allocation_guard();
