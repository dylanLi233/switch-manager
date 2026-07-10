CREATE TABLE credentials (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    credential_type text NOT NULL,
    username text NOT NULL,
    encrypted_secret bytea,
    encrypted_private_key bytea,
    encrypted_passphrase bytea,
    key_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT credentials_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT credentials_username_not_blank CHECK (btrim(username) <> ''),
    CONSTRAINT credentials_key_version_not_blank CHECK (btrim(key_version) <> ''),
    CONSTRAINT credentials_type_valid CHECK (
        credential_type IN ('PASSWORD', 'SSH_PRIVATE_KEY')
    ),
    CONSTRAINT credentials_material_valid CHECK (
        (
            credential_type = 'PASSWORD'
            AND encrypted_secret IS NOT NULL
            AND octet_length(encrypted_secret) > 0
            AND encrypted_private_key IS NULL
            AND encrypted_passphrase IS NULL
        )
        OR
        (
            credential_type = 'SSH_PRIVATE_KEY'
            AND encrypted_secret IS NULL
            AND encrypted_private_key IS NOT NULL
            AND octet_length(encrypted_private_key) > 0
            AND (
                encrypted_passphrase IS NULL
                OR octet_length(encrypted_passphrase) > 0
            )
        )
    ),
    CONSTRAINT credentials_timestamps_valid CHECK (
        updated_at >= created_at
        AND (deleted_at IS NULL OR deleted_at >= created_at)
    )
);

CREATE UNIQUE INDEX credentials_name_active_uq
    ON credentials(name)
    WHERE deleted_at IS NULL;

CREATE TABLE switches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    host text NOT NULL,
    ssh_port integer NOT NULL DEFAULT 22,
    credential_id uuid NOT NULL REFERENCES credentials(id),
    vendor text NOT NULL,
    model text NOT NULL DEFAULT '',
    os_version text NOT NULL DEFAULT '',
    detect_mode text NOT NULL DEFAULT 'AUTO',
    identity_status text NOT NULL DEFAULT 'UNKNOWN',
    status text NOT NULL DEFAULT 'ACTIVE',
    last_connected_at timestamptz,
    last_detected_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT switches_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT switches_host_not_blank CHECK (btrim(host) <> ''),
    CONSTRAINT switches_ssh_port_valid CHECK (ssh_port BETWEEN 1 AND 65535),
    CONSTRAINT switches_vendor_valid CHECK (vendor IN ('HUAWEI', 'H3C')),
    CONSTRAINT switches_detect_mode_valid CHECK (detect_mode IN ('AUTO', 'MANUAL')),
    CONSTRAINT switches_identity_status_valid CHECK (
        identity_status IN ('UNKNOWN', 'VERIFIED', 'MISMATCH', 'UNSUPPORTED')
    ),
    CONSTRAINT switches_status_valid CHECK (
        status IN ('ACTIVE', 'DISABLED', 'UNREACHABLE')
    ),
    CONSTRAINT switches_timestamps_valid CHECK (
        updated_at >= created_at
        AND (deleted_at IS NULL OR deleted_at >= created_at)
    )
);

CREATE UNIQUE INDEX switches_host_port_active_uq
    ON switches(host, ssh_port)
    WHERE deleted_at IS NULL;

CREATE INDEX switches_vendor_idx ON switches(vendor);
CREATE INDEX switches_status_idx ON switches(status);
CREATE INDEX switches_identity_status_idx ON switches(identity_status);
CREATE INDEX switches_credential_idx ON switches(credential_id);

CREATE TABLE switch_capabilities (
    device_id uuid NOT NULL REFERENCES switches(id),
    capability text NOT NULL,
    support_level text NOT NULL,
    source_plugin_version text NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, capability),
    CONSTRAINT switch_capabilities_name_not_blank CHECK (btrim(capability) <> ''),
    CONSTRAINT switch_capabilities_plugin_version_not_blank CHECK (
        btrim(source_plugin_version) <> ''
    ),
    CONSTRAINT switch_capabilities_support_level_valid CHECK (
        support_level IN ('SUPPORTED', 'EXPERIMENTAL', 'UNSUPPORTED')
    )
);

CREATE TABLE plugin_registry (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin_name text NOT NULL,
    vendor text NOT NULL,
    plugin_version text NOT NULL,
    sdk_version text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plugin_name, plugin_version),
    CONSTRAINT plugin_registry_name_not_blank CHECK (btrim(plugin_name) <> ''),
    CONSTRAINT plugin_registry_vendor_valid CHECK (vendor IN ('HUAWEI', 'H3C')),
    CONSTRAINT plugin_registry_version_not_blank CHECK (btrim(plugin_version) <> ''),
    CONSTRAINT plugin_registry_sdk_version_not_blank CHECK (btrim(sdk_version) <> ''),
    CONSTRAINT plugin_registry_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT plugin_registry_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX plugin_registry_enabled_vendor_uq
    ON plugin_registry(vendor)
    WHERE enabled;
