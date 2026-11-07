-- Stage 12.4: append-only history of confirmed bulk edits and explicit undo metadata.
CREATE TABLE bulk_change_operations
(
    id                 uuid PRIMARY KEY        DEFAULT gen_random_uuid(),
    revision_id        uuid           NOT NULL REFERENCES project_revisions (id),
    actor_id           uuid           NOT NULL REFERENCES users (id),
    action             text           NOT NULL CHECK (action IN ('price_fixed', 'price_percent', 'location')),
    value              text           NOT NULL DEFAULT '',
    before_total       numeric(20, 2) NOT NULL,
    after_total        numeric(20, 2) NOT NULL,
    post_revision_hash char(64)       NOT NULL CHECK (post_revision_hash ~ '^[a-f0-9]{64}$'),
    created_at         timestamptz    NOT NULL DEFAULT now(),
    reverted_at        timestamptz,
    reverted_by        uuid REFERENCES users (id),
    CHECK ((reverted_at IS NULL) = (reverted_by IS NULL))
);
CREATE INDEX bulk_change_operations_revision_idx ON bulk_change_operations (revision_id, created_at DESC, id DESC);
CREATE TABLE bulk_change_items
(
    operation_id          uuid           NOT NULL REFERENCES bulk_change_operations (id),
    item_id               uuid           NOT NULL, -- snapshot survives later deletion of the original work item
    old_price             numeric(20, 4) NOT NULL,
    new_price             numeric(20, 4) NOT NULL,
    old_amount            numeric(20, 2) NOT NULL,
    new_amount            numeric(20, 2) NOT NULL,
    old_location_id       uuid,
    new_location_id       uuid,
    old_manually_modified boolean        NOT NULL,
    new_manually_modified boolean        NOT NULL,
    PRIMARY KEY (operation_id, item_id)
);
-- Audited operation records cannot be deleted or rewritten. Only first undo mark is allowed.
CREATE FUNCTION guard_bulk_audit() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'bulk audit is immutable'; END IF;
    IF TG_OP = 'UPDATE' THEN
        IF OLD.reverted_at IS NOT NULL OR NEW.reverted_at IS NULL OR NEW.reverted_by IS NULL OR
           OLD.id IS DISTINCT FROM NEW.id OR OLD.revision_id IS DISTINCT FROM NEW.revision_id OR
           OLD.actor_id IS DISTINCT FROM NEW.actor_id OR OLD.action IS DISTINCT FROM NEW.action OR
           OLD.value IS DISTINCT FROM NEW.value OR OLD.before_total IS DISTINCT FROM NEW.before_total OR
           OLD.after_total IS DISTINCT FROM NEW.after_total OR
           OLD.post_revision_hash IS DISTINCT FROM NEW.post_revision_hash OR
           OLD.created_at IS DISTINCT FROM NEW.created_at THEN
            RAISE EXCEPTION 'bulk audit is immutable except first undo mark';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER bulk_change_operations_guard
    BEFORE UPDATE OR DELETE
    ON bulk_change_operations
    FOR EACH ROW
EXECUTE FUNCTION guard_bulk_audit();
CREATE FUNCTION guard_bulk_audit_items() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'bulk audit items are immutable';
END
$$;
CREATE TRIGGER bulk_change_items_guard
    BEFORE UPDATE OR DELETE
    ON bulk_change_items
    FOR EACH ROW
EXECUTE FUNCTION guard_bulk_audit_items();
