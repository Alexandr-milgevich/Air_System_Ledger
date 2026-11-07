-- Stage 4: workers assigned to projects independently from estimate lines.
CREATE TABLE project_workers
(
    project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    worker_id  uuid        NOT NULL REFERENCES workers (id) ON DELETE RESTRICT,
    added_by   uuid REFERENCES users (id),
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, worker_id)
);
CREATE INDEX idx_project_workers_worker ON project_workers (worker_id);
CREATE INDEX idx_revisions_project ON project_revisions (project_id, number DESC);
CREATE INDEX idx_systems_revision ON project_systems (revision_id, sort_order);
CREATE INDEX idx_locations_revision ON project_locations (revision_id, name);
