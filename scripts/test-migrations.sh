#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATABASE_URL="${TEST_DATABASE_DSN:-${DATABASE_URL:-}}"
if [[ -z "${DATABASE_URL}" ]]; then
  echo "TEST_DATABASE_DSN or DATABASE_URL is required" >&2
  exit 1
fi
export DATABASE_URL

psql_cmd=(psql "${DATABASE_URL}" -X -q -v ON_ERROR_STOP=1)

scalar() {
  "${psql_cmd[@]}" -Atc "$1"
}

assert_eq() {
  local expected="$1" actual="$2" label="$3"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "assertion failed: ${label}: expected=${expected} actual=${actual}" >&2
    exit 1
  fi
}

expect_failure() {
  local label="$1" sql="$2"
  if "${psql_cmd[@]}" -c "${sql}" >/dev/null 2>&1; then
    echo "expected SQL failure: ${label}" >&2
    exit 1
  fi
}

bash "${ROOT_DIR}/scripts/migrate.sh" down all >/dev/null || true
bash "${ROOT_DIR}/scripts/migrate.sh" up

assert_eq "3" "$(scalar "SELECT count(*) FROM schema_migrations")" "migration count"
assert_eq "3" "$(scalar "SELECT count(*) FROM roles")" "role seed count"
assert_eq "14" "$(scalar "SELECT count(*) FROM permissions")" "permission seed count"
assert_eq "14" "$(scalar "SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'ADMIN'")" "admin permissions"
assert_eq "4" "$(scalar "SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'VIEWER'")" "viewer permissions"
assert_eq "4" "$(scalar "SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'AUDITOR'")" "auditor permissions"

user_id="$(scalar "INSERT INTO users(external_subject, username) VALUES ('migration-test-user', 'migration-test') RETURNING id")"
admin_role_id="$(scalar "SELECT id FROM roles WHERE name = 'ADMIN'")"

"${psql_cmd[@]}" -c "INSERT INTO user_role_bindings(user_id, role_id, scope_type, scope_id) VALUES ('${user_id}', '${admin_role_id}', 'GLOBAL', '')" >/dev/null
expect_failure "GLOBAL scope with non-empty scope_id" "INSERT INTO user_role_bindings(user_id, role_id, scope_type, scope_id) VALUES ('${user_id}', '${admin_role_id}', 'GLOBAL', 'project-1')"

expect_failure "password credential with private key" "INSERT INTO credentials(name, credential_type, username, encrypted_secret, encrypted_private_key, key_version) VALUES ('bad-cred', 'PASSWORD', 'admin', decode('aa','hex'), decode('bb','hex'), 'v1')"

credential_id="$(scalar "INSERT INTO credentials(name, credential_type, username, encrypted_secret, key_version) VALUES ('valid-password', 'PASSWORD', 'admin', decode('aa','hex'), 'v1') RETURNING id")"
switch_id="$(scalar "INSERT INTO switches(name, host, ssh_port, credential_id, vendor, identity_status) VALUES ('sw-1', '192.0.2.10', 22, '${credential_id}', 'HUAWEI', 'VERIFIED') RETURNING id")"
expect_failure "duplicate active switch host and port" "INSERT INTO switches(name, host, ssh_port, credential_id, vendor) VALUES ('sw-duplicate', '192.0.2.10', 22, '${credential_id}', 'HUAWEI')"
"${psql_cmd[@]}" -c "UPDATE switches SET deleted_at = now() WHERE id = '${switch_id}'" >/dev/null
"${psql_cmd[@]}" -c "INSERT INTO switches(name, host, ssh_port, credential_id, vendor) VALUES ('sw-replacement', '192.0.2.10', 22, '${credential_id}', 'HUAWEI')" >/dev/null

expect_failure "pending task with started_at" "INSERT INTO tasks(task_type, operation, target_type, target_id, status, execution_mode, created_by, started_at) VALUES ('OPERATION', 'vlan.create', 'switch', 'sw-1', 'PENDING', 'SYNC', '${user_id}', now())"

task_id="$(scalar "INSERT INTO tasks(task_type, operation, target_type, target_id, status, execution_mode, created_by, idempotency_key) VALUES ('OPERATION', 'vlan.create', 'switch', 'sw-1', 'PENDING', 'SYNC', '${user_id}', 'idem-1') RETURNING id")"
expect_failure "duplicate actor idempotency key" "INSERT INTO tasks(task_type, operation, target_type, target_id, status, execution_mode, created_by, idempotency_key) VALUES ('OPERATION', 'vlan.create', 'switch', 'sw-2', 'PENDING', 'SYNC', '${user_id}', 'idem-1')"

"${psql_cmd[@]}" -c "INSERT INTO audit_logs(request_id, task_id, actor_user_id, actor_username, actor_role, action, target_type, target_id, status) VALUES ('req-1', '${task_id}', '${user_id}', 'migration-test', 'ADMIN', 'vlan.create', 'switch', 'sw-1', 'PENDING')" >/dev/null
expect_failure "historical audit prevents user deletion" "DELETE FROM users WHERE id = '${user_id}'"

bash "${ROOT_DIR}/scripts/migrate.sh" up >/dev/null
assert_eq "3" "$(scalar "SELECT count(*) FROM schema_migrations")" "idempotent up"

bash "${ROOT_DIR}/scripts/migrate.sh" down all
assert_eq "0" "$(scalar "SELECT count(*) FROM schema_migrations")" "down migration count"
assert_eq "" "$(scalar "SELECT to_regclass('public.roles')")" "roles dropped"
assert_eq "" "$(scalar "SELECT to_regclass('public.switches')")" "switches dropped"
assert_eq "" "$(scalar "SELECT to_regclass('public.tasks')")" "tasks dropped"

bash "${ROOT_DIR}/scripts/migrate.sh" up >/dev/null
assert_eq "3" "$(scalar "SELECT count(*) FROM schema_migrations")" "reapply migration count"

echo "migration integration tests passed"
