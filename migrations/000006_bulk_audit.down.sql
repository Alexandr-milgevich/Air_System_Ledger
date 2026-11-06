DROP TRIGGER IF EXISTS bulk_change_items_guard ON bulk_change_items;
DROP FUNCTION IF EXISTS guard_bulk_audit_items();
DROP TRIGGER IF EXISTS bulk_change_operations_guard ON bulk_change_operations;
DROP FUNCTION IF EXISTS guard_bulk_audit();
DROP TABLE IF EXISTS bulk_change_items;
DROP TABLE IF EXISTS bulk_change_operations;
