CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT roles_name_not_blank CHECK (btrim(name) <> '')
);

CREATE TABLE permissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT permissions_name_not_blank CHECK (btrim(name) <> '')
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_subject text NOT NULL UNIQUE,
    username text NOT NULL,
    status text NOT NULL DEFAULT 'ACTIVE',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_external_subject_not_blank CHECK (btrim(external_subject) <> ''),
    CONSTRAINT users_username_not_blank CHECK (btrim(username) <> ''),
    CONSTRAINT users_status_valid CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT users_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE user_role_bindings (
    user_id uuid NOT NULL REFERENCES users(id),
    role_id uuid NOT NULL REFERENCES roles(id),
    scope_type text NOT NULL DEFAULT 'GLOBAL',
    scope_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id, scope_type, scope_id),
    CONSTRAINT user_role_bindings_scope_type_valid CHECK (
        scope_type IN (
            'GLOBAL', 'ENVIRONMENT', 'PROJECT', 'RESOURCE_GROUP',
            'SWITCH_GROUP', 'SPECIFIC_RESOURCE'
        )
    ),
    CONSTRAINT user_role_bindings_scope_pair_valid CHECK (
        (scope_type = 'GLOBAL' AND scope_id = '')
        OR (scope_type <> 'GLOBAL' AND btrim(scope_id) <> '')
    )
);

INSERT INTO roles(name) VALUES
    ('VIEWER'),
    ('ADMIN'),
    ('AUDITOR')
ON CONFLICT (name) DO NOTHING;

INSERT INTO permissions(name) VALUES
    ('device.read'),
    ('device.manage'),
    ('credential.manage'),
    ('operation.query'),
    ('operation.config'),
    ('operation.custom_read'),
    ('operation.custom_config'),
    ('config.backup'),
    ('config.restore'),
    ('task.read'),
    ('task.cancel'),
    ('audit.read'),
    ('audit.export'),
    ('plugin.manage')
ON CONFLICT (name) DO NOTHING;

INSERT INTO role_permissions(role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'ADMIN'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'device.read',
    'operation.query',
    'operation.custom_read',
    'task.read'
)
WHERE r.name = 'VIEWER'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'device.read',
    'task.read',
    'audit.read',
    'audit.export'
)
WHERE r.name = 'AUDITOR'
ON CONFLICT DO NOTHING;
