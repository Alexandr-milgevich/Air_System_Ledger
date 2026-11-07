-- Sessions and database-enforced referential integrity for auth.
CREATE TABLE user_sessions
(
    id         uuid PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    csrf_hash  bytea       NOT NULL CHECK (octet_length(csrf_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE INDEX idx_user_sessions_user ON user_sessions (user_id);
CREATE INDEX idx_user_sessions_expires ON user_sessions (expires_at);
CREATE INDEX idx_projects_customer ON projects (customer_id);
CREATE INDEX idx_project_members_user ON project_members (user_id);
-- Prevent stale/unsafe status changes; all approved revisions remain final.
CREATE FUNCTION protect_revision_status() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF OLD.status IN ('approved', 'archived') THEN
        RAISE EXCEPTION 'final revision status is immutable';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'draft' AND NEW.status IN ('in_review', 'archived')) OR
        (OLD.status = 'in_review' AND NEW.status IN ('draft', 'approved', 'archived'))
        ) THEN
        RAISE EXCEPTION 'invalid revision transition';
    END IF;
    IF NEW.status = 'approved' AND (NEW.approved_at IS NULL OR NEW.approved_by IS NULL) THEN
        RAISE EXCEPTION 'approval metadata required';
    END IF;
    IF NEW.status <> 'approved' AND (NEW.approved_at IS NOT NULL OR NEW.approved_by IS NOT NULL) THEN
        RAISE EXCEPTION 'approval metadata only allowed upon approval';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER project_revisions_status_guard
    BEFORE UPDATE
    ON project_revisions
    FOR EACH ROW
EXECUTE FUNCTION protect_revision_status();
-- Event records are append-only: corrections are new entries, not edits.
CREATE FUNCTION reject_execution_event_mutation() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'execution events are append-only';
END;
$$;
CREATE TRIGGER execution_events_append_only
    BEFORE UPDATE OR DELETE
    ON work_execution_events
    FOR EACH ROW
EXECUTE FUNCTION reject_execution_event_mutation();
