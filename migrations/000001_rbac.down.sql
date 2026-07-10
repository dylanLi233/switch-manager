DROP TABLE IF EXISTS user_role_bindings;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
-- pgcrypto is intentionally retained because extensions may be shared by other schemas.
