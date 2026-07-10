CREATE TABLE tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_task_id uuid REFERENCES tasks(id),
    task_type text NOT NULL,
    operation text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    status text NOT NULL,
    execution_mode text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    save_config boolean NOT NULL DEFAULT false,
    idempotency_key text,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb,
    error_code text,
    created_by uuid NOT NULL REFERENCES users(id),
    retry_of uuid REFERENCES tasks(id),
    plugin_name text,
    plugin_version text,
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT tasks_task_type_valid CHECK (
        task_type IN ('OPERATION', 'BATCH_PARENT', 'BATCH_CHILD')
    ),
    CONSTRAINT tasks_operation_not_blank CHECK (btrim(operation) <> ''),
    CONSTRAINT tasks_target_not_blank CHECK (
        btrim(target_type) <> '' AND btrim(target_id) <> ''
    ),
    CONSTRAINT tasks_status_valid CHECK (
        status IN (
            'PENDING', 'QUEUED', 'RUNNING', 'SUCCESS',
            'PARTIAL_SUCCESS', 'FAILED', 'CANCELLED', 'INTERRUPTED'
        )
    ),
    CONSTRAINT tasks_execution_mode_valid CHECK (execution_mode IN ('SYNC', 'ASYNC')),
    CONSTRAINT tasks_idempotency_key_valid CHECK (
        idempotency_key IS NULL OR btrim(idempotency_key) <> ''
    ),
    CONSTRAINT tasks_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT tasks_result_valid CHECK (
        result IS NULL OR jsonb_typeof(result) IN ('object', 'array')
    ),
    CONSTRAINT tasks_plugin_pair_valid CHECK (
        (plugin_name IS NULL AND plugin_version IS NULL)
        OR (
            btrim(coalesce(plugin_name, '')) <> ''
            AND btrim(coalesce(plugin_version, '')) <> ''
        )
    ),
    CONSTRAINT tasks_version_valid CHECK (version >= 1),
    CONSTRAINT tasks_retry_not_self CHECK (retry_of IS NULL OR retry_of <> id),
    CONSTRAINT tasks_lifecycle_valid CHECK (
        (
            status IN ('PENDING', 'QUEUED')
            AND started_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            status = 'RUNNING'
            AND started_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            status = 'CANCELLED'
            AND finished_at IS NOT NULL
            AND (started_at IS NULL OR finished_at >= started_at)
        )
        OR (
            status IN ('SUCCESS', 'PARTIAL_SUCCESS', 'FAILED', 'INTERRUPTED')
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
            AND finished_at >= started_at
        )
    ),
    CONSTRAINT tasks_error_code_valid CHECK (
        (
            status IN ('FAILED', 'PARTIAL_SUCCESS')
            AND btrim(coalesce(error_code, '')) <> ''
        )
        OR (
            status = 'SUCCESS'
            AND btrim(coalesce(error_code, '')) = ''
        )
        OR status NOT IN ('FAILED', 'PARTIAL_SUCCESS', 'SUCCESS')
    )
);

CREATE UNIQUE INDEX tasks_actor_idempotency_uq
    ON tasks(created_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX tasks_status_created_idx ON tasks(status, created_at);
CREATE INDEX tasks_target_created_idx ON tasks(target_type, target_id, created_at DESC);
CREATE INDEX tasks_parent_idx ON tasks(parent_task_id);
CREATE INDEX tasks_retry_of_idx ON tasks(retry_of);

CREATE TABLE task_executions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES tasks(id),
    attempt_no integer NOT NULL,
    status text NOT NULL,
    worker_id text,
    plugin_name text,
    plugin_version text,
    result_summary jsonb,
    error_code text,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    UNIQUE (task_id, attempt_no),
    CONSTRAINT task_executions_attempt_valid CHECK (attempt_no >= 1),
    CONSTRAINT task_executions_status_valid CHECK (
        status IN ('RUNNING', 'SUCCESS', 'PARTIAL_SUCCESS', 'FAILED', 'INTERRUPTED')
    ),
    CONSTRAINT task_executions_worker_not_blank CHECK (
        worker_id IS NULL OR btrim(worker_id) <> ''
    ),
    CONSTRAINT task_executions_result_valid CHECK (
        result_summary IS NULL OR jsonb_typeof(result_summary) = 'object'
    ),
    CONSTRAINT task_executions_timestamps_valid CHECK (
        finished_at IS NULL OR finished_at >= started_at
    )
);

CREATE INDEX task_executions_task_idx ON task_executions(task_id, attempt_no DESC);

CREATE TABLE batch_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_task_id uuid NOT NULL UNIQUE REFERENCES tasks(id),
    operation text NOT NULL,
    continue_on_failure boolean NOT NULL DEFAULT true,
    total_count integer NOT NULL,
    success_count integer NOT NULL DEFAULT 0,
    failed_count integer NOT NULL DEFAULT 0,
    cancelled_count integer NOT NULL DEFAULT 0,
    status text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT batch_tasks_operation_not_blank CHECK (btrim(operation) <> ''),
    CONSTRAINT batch_tasks_counts_valid CHECK (
        total_count >= 1
        AND success_count >= 0
        AND failed_count >= 0
        AND cancelled_count >= 0
        AND success_count + failed_count + cancelled_count <= total_count
    ),
    CONSTRAINT batch_tasks_status_valid CHECK (
        status IN (
            'PENDING', 'QUEUED', 'RUNNING', 'SUCCESS',
            'PARTIAL_SUCCESS', 'FAILED', 'CANCELLED', 'INTERRUPTED'
        )
    ),
    CONSTRAINT batch_tasks_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE batch_task_items (
    batch_task_id uuid NOT NULL REFERENCES batch_tasks(id),
    device_id uuid NOT NULL REFERENCES switches(id),
    child_task_id uuid NOT NULL UNIQUE REFERENCES tasks(id),
    sequence_no integer NOT NULL,
    PRIMARY KEY (batch_task_id, device_id),
    UNIQUE (batch_task_id, sequence_no),
    CONSTRAINT batch_task_items_sequence_valid CHECK (sequence_no >= 1)
);

CREATE TABLE audit_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id text NOT NULL,
    task_id uuid REFERENCES tasks(id),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    actor_username text NOT NULL,
    actor_role text NOT NULL,
    service_actor_id text,
    source_ip inet,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    device_vendor text,
    device_model text,
    device_os_version text,
    plugin_name text,
    plugin_version text,
    request_payload_redacted jsonb,
    command_plan_redacted jsonb,
    result_summary_redacted jsonb,
    status text NOT NULL,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CONSTRAINT audit_logs_request_id_not_blank CHECK (btrim(request_id) <> ''),
    CONSTRAINT audit_logs_actor_not_blank CHECK (
        btrim(actor_username) <> '' AND actor_role IN ('VIEWER', 'ADMIN', 'AUDITOR')
    ),
    CONSTRAINT audit_logs_action_target_not_blank CHECK (
        btrim(action) <> '' AND btrim(target_type) <> '' AND btrim(target_id) <> ''
    ),
    CONSTRAINT audit_logs_vendor_valid CHECK (
        device_vendor IS NULL OR device_vendor IN ('HUAWEI', 'H3C')
    ),
    CONSTRAINT audit_logs_json_valid CHECK (
        (request_payload_redacted IS NULL OR jsonb_typeof(request_payload_redacted) = 'object')
        AND (command_plan_redacted IS NULL OR jsonb_typeof(command_plan_redacted) IN ('object', 'array'))
        AND (result_summary_redacted IS NULL OR jsonb_typeof(result_summary_redacted) = 'object')
    ),
    CONSTRAINT audit_logs_status_not_blank CHECK (btrim(status) <> ''),
    CONSTRAINT audit_logs_timestamps_valid CHECK (
        finished_at IS NULL OR finished_at >= created_at
    )
);

CREATE INDEX audit_logs_actor_created_idx
    ON audit_logs(actor_user_id, created_at DESC);
CREATE INDEX audit_logs_target_created_idx
    ON audit_logs(target_type, target_id, created_at DESC);
CREATE INDEX audit_logs_task_idx ON audit_logs(task_id);
CREATE INDEX audit_logs_request_idx ON audit_logs(request_id);

CREATE TABLE config_backups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id uuid NOT NULL REFERENCES switches(id),
    vendor text NOT NULL,
    model text NOT NULL DEFAULT '',
    os_version text NOT NULL DEFAULT '',
    plugin_name text NOT NULL,
    plugin_version text NOT NULL,
    file_path text NOT NULL,
    sha256 char(64) NOT NULL,
    file_size bigint NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    task_id uuid NOT NULL REFERENCES tasks(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT config_backups_vendor_valid CHECK (vendor IN ('HUAWEI', 'H3C')),
    CONSTRAINT config_backups_plugin_not_blank CHECK (
        btrim(plugin_name) <> '' AND btrim(plugin_version) <> ''
    ),
    CONSTRAINT config_backups_path_safe CHECK (
        btrim(file_path) <> ''
        AND file_path !~ '^/'
        AND file_path !~ '(^|/)\.\.(/|$)'
    ),
    CONSTRAINT config_backups_sha256_valid CHECK (sha256 ~ '^[0-9a-fA-F]{64}$'),
    CONSTRAINT config_backups_file_size_valid CHECK (file_size >= 0)
);

CREATE INDEX config_backups_device_created_idx
    ON config_backups(device_id, created_at DESC);
CREATE INDEX config_backups_task_idx ON config_backups(task_id);
