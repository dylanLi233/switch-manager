CREATE FUNCTION prevent_task_request_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.parent_task_id IS DISTINCT FROM OLD.parent_task_id
       OR NEW.task_type IS DISTINCT FROM OLD.task_type
       OR NEW.operation IS DISTINCT FROM OLD.operation
       OR NEW.target_type IS DISTINCT FROM OLD.target_type
       OR NEW.target_id IS DISTINCT FROM OLD.target_id
       OR NEW.execution_mode IS DISTINCT FROM OLD.execution_mode
       OR NEW.dry_run IS DISTINCT FROM OLD.dry_run
       OR NEW.save_config IS DISTINCT FROM OLD.save_config
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.retry_of IS DISTINCT FROM OLD.retry_of
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'task request snapshot is immutable'
            USING ERRCODE = '23503',
                  CONSTRAINT = 'tasks_request_immutable_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_request_immutable_guard
BEFORE UPDATE ON tasks
FOR EACH ROW
EXECUTE FUNCTION prevent_task_request_mutation();
