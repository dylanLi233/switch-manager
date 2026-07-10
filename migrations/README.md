# Database migrations

Migrations are applied in numeric order by `scripts/migrate.sh`.

## Commands

```bash
DATABASE_URL='postgres://...' bash ./scripts/migrate.sh up
DATABASE_URL='postgres://...' bash ./scripts/migrate.sh down 1
DATABASE_URL='postgres://...' bash ./scripts/migrate.sh down all
```

## Rules

- Each migration runs in its own PostgreSQL transaction.
- Applied versions are recorded in `schema_migrations`.
- Re-running `up` is safe; already applied versions are skipped.
- `down` is intended for local development and empty-database CI verification.
- Production rollback must be reviewed separately before execution because down migrations drop data.
- Historical task, audit, and backup foreign keys intentionally use PostgreSQL's default `NO ACTION`; they are never cascade-deleted.
- `pgcrypto` is not removed by down migrations because an extension can be shared by other schemas.
