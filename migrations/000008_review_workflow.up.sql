-- Stage 12.6: review workflow and immutable decision trail.
CREATE TABLE revision_review_events
(
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    revision_id uuid        NOT NULL REFERENCES project_revisions (id),
    from_status text        NOT NULL CHECK (from_status IN ('draft', 'in_review', 'approved', 'archived')),
    to_status   text        NOT NULL CHECK (to_status IN ('draft', 'in_review', 'approved', 'archived')),
    actor_id    uuid REFERENCES users (id),
    comment     text        NOT NULL DEFAULT '' CHECK (length(comment) <= 2000),
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (from_status <> to_status)
);
CREATE INDEX revision_review_events_revision ON revision_review_events (revision_id, id DESC);
CREATE FUNCTION revision_review_audit_trigger() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    v_actor uuid; v_comment text;
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        v_actor := NULLIF(current_setting('app.actor_id', true), '')::uuid;
        v_comment := COALESCE(NULLIF(current_setting('app.workflow_comment', true), ''), '');
        INSERT INTO revision_review_events(revision_id, from_status, to_status, actor_id, comment)
        VALUES (NEW.id, OLD.status, NEW.status, v_actor, v_comment);
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER revision_review_audit
    AFTER UPDATE OF status
    ON project_revisions
    FOR EACH ROW
EXECUTE FUNCTION revision_review_audit_trigger();
CREATE FUNCTION revision_review_events_immutable() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'revision review events are immutable';
END
$$;
CREATE TRIGGER revision_review_events_guard
    BEFORE UPDATE OR DELETE
    ON revision_review_events
    FOR EACH ROW
EXECUTE FUNCTION revision_review_events_immutable();

-- Block writes while in_review as well as after approval. Lock parent to serialize
-- edits with the transition. Preserve checks of OLD and NEW revision identities.
CREATE OR REPLACE FUNCTION guard_revision_content() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    previous_status text; next_status text;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        SELECT status INTO previous_status FROM project_revisions WHERE id = OLD.revision_id FOR UPDATE;
        IF previous_status IS NULL OR previous_status <> 'draft' THEN
            RAISE EXCEPTION 'previous revision is not editable';
        END IF;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        SELECT status INTO next_status FROM project_revisions WHERE id = NEW.revision_id FOR UPDATE;
        IF next_status IS NULL OR next_status <> 'draft' THEN
            RAISE EXCEPTION 'next revision is not editable';
        END IF;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END
$$;
