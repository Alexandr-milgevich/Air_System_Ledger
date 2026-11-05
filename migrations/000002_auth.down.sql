DROP TRIGGER IF EXISTS execution_events_append_only ON work_execution_events;
DROP FUNCTION IF EXISTS reject_execution_event_mutation();
DROP TRIGGER IF EXISTS project_revisions_status_guard ON project_revisions;
DROP FUNCTION IF EXISTS protect_revision_status();
DROP INDEX IF EXISTS idx_project_members_user;
DROP INDEX IF EXISTS idx_projects_customer;
DROP TABLE IF EXISTS user_sessions;
