DROP TRIGGER IF EXISTS stage10_approval_payment_check ON work_execution_approvals;
DROP FUNCTION IF EXISTS stage10_approval_payment_guard();
DROP TRIGGER IF EXISTS stage10_payment_insert ON project_worker_payments;
DROP FUNCTION IF EXISTS stage10_payment_guard();
DROP FUNCTION IF EXISTS stage10_worker_accrued(uuid, uuid, uuid);
DROP TRIGGER IF EXISTS stage10_payment_no_mutation ON project_worker_payments;
DROP FUNCTION IF EXISTS stage10_payment_immutable();
DROP TABLE IF EXISTS project_worker_payments;
