DROP TRIGGER IF EXISTS stage131_schedule_events_guard ON work_assignment_schedule_events;
DROP FUNCTION IF EXISTS stage131_schedule_events_immutable();
DROP TRIGGER IF EXISTS stage131_schedule_changed ON work_assignment_schedules;
DROP FUNCTION IF EXISTS stage131_schedule_audit();
DROP TABLE IF EXISTS work_assignment_schedule_events;
DROP TABLE IF EXISTS work_assignment_schedules;
