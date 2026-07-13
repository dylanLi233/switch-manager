CREATE TABLE IF NOT EXISTS batch_tasks (
    id uuid PRIMARY KEY,
    parent_task_id uuid NOT NULL UNIQUE REFERENCES tasks(id),
    operation text NOT NULL,
    continue_on_failure boolean NOT NULL DEFAULT true,
    total_count integer NOT NULL CHECK (total_count > 0),
    success_count integer NOT NULL DEFAULT 0 CHECK (success_count >= 0),
    failed_count integer NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    cancelled_count integer NOT NULL DEFAULT 0 CHECK (cancelled_count >= 0),
    status text NOT NULL CHECK (status IN (
        'PENDING','QUEUED','RUNNING','SUCCESS','PARTIAL_SUCCESS','FAILED','CANCELLED','INTERRUPTED'
    )),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (success_count + failed_count + cancelled_count <= total_count)
);

CREATE INDEX IF NOT EXISTS batch_tasks_status_created_idx
    ON batch_tasks(status, created_at, id);

CREATE TABLE IF NOT EXISTS batch_task_items (
    batch_task_id uuid NOT NULL REFERENCES batch_tasks(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES switches(id),
    child_task_id uuid NOT NULL UNIQUE REFERENCES tasks(id),
    sequence_no integer NOT NULL CHECK (sequence_no > 0),
    PRIMARY KEY (batch_task_id, device_id),
    UNIQUE (batch_task_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS batch_task_items_batch_sequence_idx
    ON batch_task_items(batch_task_id, sequence_no);
