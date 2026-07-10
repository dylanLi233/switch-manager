#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  DATABASE_URL=postgres://... scripts/migrate.sh up
  DATABASE_URL=postgres://... scripts/migrate.sh down [steps|all]
EOF
}

if ! command -v psql >/dev/null 2>&1; then
  echo "psql is required" >&2
  exit 1
fi

DATABASE_URL="${DATABASE_URL:-${TEST_DATABASE_DSN:-}}"
if [[ -z "${DATABASE_URL}" ]]; then
  echo "DATABASE_URL or TEST_DATABASE_DSN is required" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-${ROOT_DIR}/migrations}"
ACTION="${1:-}"

if [[ ! -d "${MIGRATIONS_DIR}" ]]; then
  echo "migration directory not found: ${MIGRATIONS_DIR}" >&2
  exit 1
fi

psql_cmd=(psql "${DATABASE_URL}" -X -q -v ON_ERROR_STOP=1)

"${psql_cmd[@]}" <<'SQL' >/dev/null
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schema_migrations_name_not_blank CHECK (btrim(name) <> '')
);
SQL

validate_filename() {
  local file="$1"
  local base
  base="$(basename "${file}")"
  if [[ ! "${base}" =~ ^([0-9]{6})_([a-z0-9_]+)\.(up|down)\.sql$ ]]; then
    echo "invalid migration filename: ${base}" >&2
    exit 1
  fi
}

apply_up() {
  local file base version name version_num applied escaped_name tmp
  shopt -s nullglob
  local files=("${MIGRATIONS_DIR}"/*.up.sql)
  if (( ${#files[@]} == 0 )); then
    echo "no up migrations found" >&2
    exit 1
  fi
  mapfile -t files < <(printf '%s\n' "${files[@]}" | sort)

  for file in "${files[@]}"; do
    validate_filename "${file}"
    base="$(basename "${file}")"
    version="${base%%_*}"
    name="${base#*_}"
    name="${name%.up.sql}"
    version_num=$((10#${version}))
    applied="$("${psql_cmd[@]}" -Atc "SELECT 1 FROM schema_migrations WHERE version = ${version_num}")"
    if [[ "${applied}" == "1" ]]; then
      echo "skip ${base}"
      continue
    fi

    escaped_name="${name//\'/\'\'}"
    tmp="$(mktemp)"
    trap 'rm -f "${tmp:-}"' RETURN
    {
      echo 'BEGIN;'
      cat "${file}"
      printf "\nINSERT INTO schema_migrations(version, name) VALUES (%d, '%s');\n" "${version_num}" "${escaped_name}"
      echo 'COMMIT;'
    } > "${tmp}"

    echo "apply ${base}"
    "${psql_cmd[@]}" -f "${tmp}" >/dev/null
    rm -f "${tmp}"
    trap - RETURN
  done
}

apply_down() {
  local requested="${1:-1}"
  local limit_clause
  if [[ "${requested}" == "all" ]]; then
    limit_clause=""
  elif [[ "${requested}" =~ ^[1-9][0-9]*$ ]]; then
    limit_clause="LIMIT ${requested}"
  else
    echo "down steps must be a positive integer or all" >&2
    exit 1
  fi

  local rows
  rows="$("${psql_cmd[@]}" -Atc "SELECT version || '|' || name FROM schema_migrations ORDER BY version DESC ${limit_clause}")"
  if [[ -z "${rows}" ]]; then
    echo "no applied migrations to revert"
    return
  fi

  local row version_num name version file tmp
  while IFS='|' read -r version_num name; do
    version="$(printf '%06d' "${version_num}")"
    file="${MIGRATIONS_DIR}/${version}_${name}.down.sql"
    if [[ ! -f "${file}" ]]; then
      echo "missing down migration: ${file}" >&2
      exit 1
    fi
    validate_filename "${file}"

    tmp="$(mktemp)"
    trap 'rm -f "${tmp:-}"' RETURN
    {
      echo 'BEGIN;'
      cat "${file}"
      printf "\nDELETE FROM schema_migrations WHERE version = %d;\n" "${version_num}"
      echo 'COMMIT;'
    } > "${tmp}"

    echo "revert $(basename "${file}")"
    "${psql_cmd[@]}" -f "${tmp}" >/dev/null
    rm -f "${tmp}"
    trap - RETURN
  done <<< "${rows}"
}

case "${ACTION}" in
  up)
    apply_up
    ;;
  down)
    apply_down "${2:-1}"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
