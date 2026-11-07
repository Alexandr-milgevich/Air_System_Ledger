-- Stage 12.9: audit log for global-user and project-permission administration.
CREATE TABLE user_admin_events
(
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id       uuid        REFERENCES users (id) ON DELETE SET NULL,
    target_user_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    action         text        NOT NULL CHECK (action IN
                                               ('created', 'updated', 'blocked', 'unblocked', 'password_changed',
                                                'project_access')),
    details        text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX user_admin_events_target_idx ON user_admin_events (target_user_id, id DESC);
CREATE FUNCTION protect_user_admin_events() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION 'user administration audit is immutable';
END;
$$;
CREATE TRIGGER user_admin_events_no_mutation
    BEFORE UPDATE OR DELETE
    ON user_admin_events
    FOR EACH ROW
EXECUTE FUNCTION protect_user_admin_events();
