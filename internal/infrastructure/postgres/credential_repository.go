package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
)

const credentialMetadataColumns = `id::text, name, credential_type, username, key_version, created_at, updated_at`

// CredentialRepository persists credentials but exposes metadata only.
type CredentialRepository struct{ q DBTX }

// CredentialExecutionRepository returns encrypted material for the execution path.
type CredentialExecutionRepository struct{ q DBTX }

// Create inserts encrypted authentication material and returns safe metadata.
func (r *CredentialRepository) Create(ctx context.Context, value credential.Credential) (credential.Metadata, error) {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	if err := value.Validate(); err != nil {
		return credential.Metadata{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		INSERT INTO credentials (
			id, name, credential_type, username, encrypted_secret,
			encrypted_private_key, encrypted_passphrase, key_version,
			created_at, updated_at
		) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+credentialMetadataColumns,
		value.ID, value.Name, string(value.Type), value.Username,
		bytesOrNil(value.EncryptedSecret), bytesOrNil(value.EncryptedPrivateKey),
		bytesOrNil(value.EncryptedPassphrase), value.KeyVersion,
		value.CreatedAt, value.UpdatedAt,
	)
	result, err := scanCredentialMetadata(row)
	return result, mapDatabaseError(err, apperror.CodeCredentialNotFound, "create credential")
}

// GetMetadata returns a non-secret active credential view.
func (r *CredentialRepository) GetMetadata(ctx context.Context, id string) (credential.Metadata, error) {
	row := r.q.QueryRow(ctx, `SELECT `+credentialMetadataColumns+` FROM credentials WHERE id=$1::uuid AND deleted_at IS NULL`, id)
	result, err := scanCredentialMetadata(row)
	return result, mapDatabaseError(err, apperror.CodeCredentialNotFound, "get credential metadata")
}

// ListMetadata returns non-secret active credential views.
func (r *CredentialRepository) ListMetadata(ctx context.Context, limit, offset int) ([]credential.Metadata, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		return nil, apperror.Wrap(apperror.CodeValidationError, "", fmt.Errorf("credential offset cannot be negative"))
	}
	rows, err := r.q.Query(ctx, `SELECT `+credentialMetadataColumns+`
		FROM credentials WHERE deleted_at IS NULL ORDER BY created_at, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, mapDatabaseError(err, "", "list credential metadata")
	}
	defer rows.Close()
	result := make([]credential.Metadata, 0)
	for rows.Next() {
		value, scanErr := scanCredentialMetadata(rows)
		if scanErr != nil {
			return nil, mapDatabaseError(scanErr, "", "scan credential metadata")
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(err, "", "iterate credential metadata")
	}
	return result, nil
}

// Update replaces encrypted authentication material and metadata.
func (r *CredentialRepository) Update(ctx context.Context, value credential.Credential) (credential.Metadata, error) {
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = time.Now().UTC()
	}
	if err := value.Validate(); err != nil {
		return credential.Metadata{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		UPDATE credentials SET
			name=$2, credential_type=$3, username=$4, encrypted_secret=$5,
			encrypted_private_key=$6, encrypted_passphrase=$7,
			key_version=$8, updated_at=$9
		WHERE id=$1::uuid AND deleted_at IS NULL
		RETURNING `+credentialMetadataColumns,
		value.ID, value.Name, string(value.Type), value.Username,
		bytesOrNil(value.EncryptedSecret), bytesOrNil(value.EncryptedPrivateKey),
		bytesOrNil(value.EncryptedPassphrase), value.KeyVersion, value.UpdatedAt,
	)
	result, err := scanCredentialMetadata(row)
	return result, mapDatabaseError(err, apperror.CodeCredentialNotFound, "update credential")
}

// SoftDelete marks a credential deleted while preserving references.
func (r *CredentialRepository) SoftDelete(ctx context.Context, id string) error {
	row := r.q.QueryRow(ctx, `UPDATE credentials SET deleted_at=now(), updated_at=now()
		WHERE id=$1::uuid AND deleted_at IS NULL RETURNING id::text`, id)
	var returned string
	return mapDatabaseError(row.Scan(&returned), apperror.CodeCredentialNotFound, "delete credential")
}

// GetForExecution returns encrypted material for one active credential.
func (r *CredentialExecutionRepository) GetForExecution(ctx context.Context, id string) (credential.Credential, error) {
	row := r.q.QueryRow(ctx, `
		SELECT id::text, name, credential_type, username, encrypted_secret,
			encrypted_private_key, encrypted_passphrase, key_version, created_at, updated_at
		FROM credentials WHERE id=$1::uuid AND deleted_at IS NULL`, id)
	var result credential.Credential
	var credentialType string
	err := row.Scan(&result.ID, &result.Name, &credentialType, &result.Username,
		&result.EncryptedSecret, &result.EncryptedPrivateKey, &result.EncryptedPassphrase,
		&result.KeyVersion, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return credential.Credential{}, mapDatabaseError(err, apperror.CodeCredentialNotFound, "get execution credential")
	}
	result.Type = credential.Type(credentialType)
	if err := result.Validate(); err != nil {
		return credential.Credential{}, mapDatabaseError(fmt.Errorf("invalid credential row: %w", err), "", "scan execution credential")
	}
	return result, nil
}

func scanCredentialMetadata(row rowScanner) (credential.Metadata, error) {
	var result credential.Metadata
	var credentialType string
	if err := row.Scan(&result.ID, &result.Name, &credentialType, &result.Username,
		&result.KeyVersion, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return credential.Metadata{}, err
	}
	result.Type = credential.Type(credentialType)
	if err := result.Type.Validate(); err != nil {
		return credential.Metadata{}, fmt.Errorf("invalid credential type in row: %w", err)
	}
	return result, nil
}

var _ credential.Repository = (*CredentialRepository)(nil)
var _ credential.ExecutionRepository = (*CredentialExecutionRepository)(nil)
