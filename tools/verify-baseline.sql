-- Stage 11 / read-only verification of manually applied migrations 000001..000005.
-- Stops immediately when any required object is missing.
DO $verify$
DECLARE obj text; required_tables text[] := ARRAY[
 'users','roles','user_roles','customers','projects','project_members',
 'project_revisions','project_systems','project_locations','workers','units',
 'work_catalog','project_work_items','work_assignments','work_execution_events',
 'worker_rate_snapshots','user_sessions','project_workers',
 'work_execution_approvals','project_worker_payments'];
BEGIN
 FOREACH obj IN ARRAY required_tables LOOP
  IF to_regclass(format('public.%I',obj)) IS NULL THEN
   RAISE EXCEPTION 'Missing required table: %',obj;
  END IF;
 END LOOP;
 IF (SELECT count(*) FROM pg_tables WHERE schemaname='public') < 20 THEN
  RAISE EXCEPTION 'Fewer than 20 application tables present';
 END IF;
 FOR obj IN SELECT unnest(ARRAY[
  'project_revisions_status_guard','execution_events_append_only',
  'stage9_event_insert','stage9_approval_append_only',
  'stage9_rate_write','stage10_payment_insert',
  'stage10_payment_no_mutation','stage10_approval_payment_check'
 ]) LOOP
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname=obj AND NOT tgisinternal) THEN
   RAISE EXCEPTION 'Missing critical trigger: %',obj;
  END IF;
 END LOOP;
 RAISE NOTICE 'Baseline objects for migrations 000001..000005 verified';
END $verify$;
