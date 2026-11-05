-- Baseline domain schema. Designed for PostgreSQL 17+.
-- Migration is a draft until run against a real PostgreSQL instance.
CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 login text NOT NULL UNIQUE CHECK (length(trim(login)) > 0),
 password_hash text NOT NULL,
 display_name text NOT NULL CHECK (length(trim(display_name)) > 0),
 disabled_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE roles (
 code text PRIMARY KEY CHECK (code IN ('admin','director','engineer','manager','installer'))
);
INSERT INTO roles(code) VALUES ('admin'),('director'),('engineer'),('manager'),('installer');
CREATE TABLE user_roles (
 user_id uuid NOT NULL REFERENCES users(id), role_code text NOT NULL REFERENCES roles(code),
 PRIMARY KEY(user_id, role_code)
);
CREATE TABLE customers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL CHECK (length(trim(name)) > 0),
 inn text, contact text, phone text, email text, address text, notes text,
 archived_at timestamptz
);
CREATE TABLE projects (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 customer_id uuid NOT NULL REFERENCES customers(id),
 name text NOT NULL CHECK (length(trim(name)) > 0),
 address text, description text,
 created_by uuid REFERENCES users(id),
 archived_at timestamptz,
 UNIQUE(id, customer_id)
);
CREATE TABLE project_members (
 project_id uuid NOT NULL REFERENCES projects(id),
 user_id uuid NOT NULL REFERENCES users(id),
 permission text NOT NULL CHECK(permission IN('read','write','approve','admin')),
 PRIMARY KEY(project_id, user_id)
);
CREATE TABLE project_revisions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 project_id uuid NOT NULL REFERENCES projects(id),
 number integer NOT NULL CHECK(number > 0),
 status text NOT NULL DEFAULT 'draft' CHECK(status IN('draft','in_review','approved','archived')),
 created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 approved_by uuid REFERENCES users(id), approved_at timestamptz,
 UNIQUE(project_id,number), UNIQUE(id,project_id),
 CHECK ((status = 'approved') = (approved_at IS NOT NULL))
);
CREATE TABLE project_systems (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), revision_id uuid NOT NULL REFERENCES project_revisions(id),
 code text NOT NULL, name text NOT NULL, type_code text NOT NULL,
 sort_order integer NOT NULL DEFAULT 0,
 UNIQUE(revision_id,id), UNIQUE(revision_id,code)
);
CREATE TABLE project_locations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), revision_id uuid NOT NULL REFERENCES project_revisions(id),
 name text NOT NULL CHECK(length(trim(name)) > 0),
 UNIQUE(revision_id,id), UNIQUE(revision_id,name)
);
CREATE TABLE workers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 display_name text NOT NULL CHECK(length(trim(display_name)) > 0),
 active boolean NOT NULL DEFAULT true,
 linked_user_id uuid UNIQUE REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE units (
 code text PRIMARY KEY,
 display_name text NOT NULL,
 dimension text NOT NULL CHECK(dimension IN('count','length','area','volume','mass','power','money','ratio'))
);
INSERT INTO units(code,display_name,dimension) VALUES
 ('pcs','шт.','count'),('m','пог. м','length'),('m2','м²','area'),('m3','м³','volume'),('kg','кг','mass'),('kw','кВт','power'),('rub','руб.','money');
CREATE TABLE work_catalog (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL, category_code text NOT NULL,
 input_unit_code text NOT NULL REFERENCES units(code),
 result_unit_code text NOT NULL REFERENCES units(code),
 billable_unit_code text NOT NULL REFERENCES units(code),
 rule_type text NOT NULL CHECK(rule_type IN('direct','coefficient','geometry_rectangular','geometry_round','reference_coefficient','sum_items','expression','manual')),
 rule_version integer NOT NULL DEFAULT 1,
 active boolean NOT NULL DEFAULT true
);
CREATE TABLE project_work_items (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 revision_id uuid NOT NULL REFERENCES project_revisions(id),
 system_id uuid NOT NULL,
 location_id uuid,
 work_catalog_id uuid REFERENCES work_catalog(id),
 name_snapshot text NOT NULL CHECK(length(trim(name_snapshot)) > 0),
 source_type text NOT NULL DEFAULT 'manual' CHECK(source_type IN('manual','equipment','template','rule')),
 input_quantity numeric(20,8) NOT NULL CHECK(input_quantity >= 0),
 input_unit_code text NOT NULL REFERENCES units(code),
 coefficient numeric(20,10),
 calculated_quantity numeric(20,8) NOT NULL CHECK(calculated_quantity >= 0),
 result_unit_code text NOT NULL REFERENCES units(code),
 billable_quantity numeric(20,8) NOT NULL CHECK(billable_quantity >= 0),
 billable_unit_code text NOT NULL REFERENCES units(code),
 unit_price numeric(20,4) NOT NULL CHECK(unit_price >= 0),
 amount numeric(20,2) NOT NULL CHECK(amount >= 0),
 currency_code text NOT NULL DEFAULT 'RUB',
 rule_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
 inputs_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
 trace_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
 manually_modified boolean NOT NULL DEFAULT false,
 notes text,
 sort_order integer NOT NULL DEFAULT 0,
 UNIQUE(revision_id,id),
 FOREIGN KEY(revision_id,system_id) REFERENCES project_systems(revision_id,id),
 FOREIGN KEY(revision_id,location_id) REFERENCES project_locations(revision_id,id)
);
CREATE INDEX idx_work_items_system ON project_work_items(revision_id,system_id,sort_order);
CREATE TABLE work_assignments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 revision_id uuid NOT NULL,
 work_item_id uuid NOT NULL,
 worker_id uuid NOT NULL REFERENCES workers(id),
 planned_quantity numeric(20,8) NOT NULL DEFAULT 0 CHECK(planned_quantity >= 0),
 share_percent numeric(7,4) CHECK(share_percent >= 0 AND share_percent <= 100),
 UNIQUE(work_item_id, worker_id),
 FOREIGN KEY(revision_id,work_item_id) REFERENCES project_work_items(revision_id,id)
);
-- Actual work is an append-only event log: corrections require compensating records.
CREATE TABLE work_execution_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 assignment_id uuid NOT NULL REFERENCES work_assignments(id),
 event_type text NOT NULL CHECK(event_type IN('performed','correction')),
 quantity_delta numeric(20,8) NOT NULL CHECK(quantity_delta <> 0),
 performed_on date NOT NULL,
 approved_by uuid REFERENCES users(id),
 created_by uuid REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 notes text
);
CREATE INDEX idx_work_events_assignment ON work_execution_events(assignment_id,performed_on);
CREATE TABLE worker_rate_snapshots (
 assignment_id uuid PRIMARY KEY REFERENCES work_assignments(id),
 rate_per_billable_unit numeric(20,4) NOT NULL CHECK(rate_per_billable_unit >= 0),
 unit_code text NOT NULL REFERENCES units(code),
 currency_code text NOT NULL DEFAULT 'RUB',
 captured_at timestamptz NOT NULL DEFAULT now()
);
-- Revision content immutability applies to changes in both OLD and NEW revision.
CREATE FUNCTION guard_revision_content() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous_status text; next_status text;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  SELECT status INTO previous_status FROM project_revisions WHERE id = OLD.revision_id FOR UPDATE;
  IF previous_status IS NULL OR previous_status IN('approved','archived') THEN
   RAISE EXCEPTION 'previous revision is immutable or missing';
  END IF;
 END IF;
 IF TG_OP <> 'DELETE' THEN
  SELECT status INTO next_status FROM project_revisions WHERE id = NEW.revision_id FOR UPDATE;
  IF next_status IS NULL OR next_status IN('approved','archived') THEN
   RAISE EXCEPTION 'next revision is immutable or missing';
  END IF;
 END IF;
 RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
CREATE TRIGGER project_systems_guard BEFORE INSERT OR UPDATE OR DELETE ON project_systems
 FOR EACH ROW EXECUTE FUNCTION guard_revision_content();
CREATE TRIGGER project_locations_guard BEFORE INSERT OR UPDATE OR DELETE ON project_locations
 FOR EACH ROW EXECUTE FUNCTION guard_revision_content();
CREATE TRIGGER project_work_items_guard BEFORE INSERT OR UPDATE OR DELETE ON project_work_items
 FOR EACH ROW EXECUTE FUNCTION guard_revision_content();
CREATE TRIGGER work_assignments_guard BEFORE INSERT OR UPDATE OR DELETE ON work_assignments
 FOR EACH ROW EXECUTE FUNCTION guard_revision_content();
-- Execution events intentionally remain writable after approval: they represent work performed,
-- not changes to the immutable estimate. Locking/authorization is application-level until Stage 2.
