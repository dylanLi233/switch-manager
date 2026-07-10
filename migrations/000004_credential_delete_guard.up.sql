CREATE FUNCTION prevent_active_credential_soft_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND EXISTS (
        SELECT 1
        FROM switches
        WHERE credential_id = OLD.id
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'credential is referenced by an active switch'
            USING ERRCODE = '23503',
                  CONSTRAINT = 'credentials_active_switches_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER credentials_active_switches_guard
BEFORE UPDATE OF deleted_at ON credentials
FOR EACH ROW
EXECUTE FUNCTION prevent_active_credential_soft_delete();
