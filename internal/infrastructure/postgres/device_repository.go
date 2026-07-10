package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

const deviceColumns = `
	id::text, name, host, ssh_port, credential_id::text, vendor,
	model, os_version, detect_mode, identity_status, status,
	last_connected_at, last_detected_at, created_at, updated_at`

// DeviceRepository persists active switch inventory.
type DeviceRepository struct{ q DBTX }

// Create inserts one active switch.
func (r *DeviceRepository) Create(ctx context.Context, value device.Device) (device.Device, error) {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	if err := value.Validate(); err != nil {
		return device.Device{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		INSERT INTO switches (
			id, name, host, ssh_port, credential_id, vendor, model, os_version,
			detect_mode, identity_status, status, last_connected_at,
			last_detected_at, created_at, updated_at
		) VALUES (
			$1::uuid, $2, $3, $4, $5::uuid, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)
		RETURNING `+deviceColumns,
		value.ID, value.Name, value.Host, value.SSHPort, value.CredentialID,
		string(value.Vendor), value.Model, value.OSVersion, string(value.DetectMode),
		string(value.IdentityStatus), string(value.Status), value.LastConnectedAt,
		value.LastDetectedAt, value.CreatedAt, value.UpdatedAt,
	)
	result, err := scanDevice(row)
	return result, mapDatabaseError(err, apperror.CodeDeviceNotFound, "create device")
}

// Get returns an active switch by ID.
func (r *DeviceRepository) Get(ctx context.Context, id string) (device.Device, error) {
	row := r.q.QueryRow(ctx, `SELECT `+deviceColumns+` FROM switches WHERE id = $1::uuid AND deleted_at IS NULL`, id)
	result, err := scanDevice(row)
	return result, mapDatabaseError(err, apperror.CodeDeviceNotFound, "get device")
}

// List returns active switches ordered by creation time and ID.
func (r *DeviceRepository) List(ctx context.Context, filter device.ListFilter) ([]device.Device, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if filter.Offset < 0 {
		return nil, apperror.Wrap(apperror.CodeValidationError, "", fmt.Errorf("device offset cannot be negative"))
	}

	conditions := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 4)
	if filter.Vendor != nil {
		if err := filter.Vendor.Validate(); err != nil {
			return nil, apperror.Wrap(apperror.CodeValidationError, "", err)
		}
		args = append(args, string(*filter.Vendor))
		conditions = append(conditions, fmt.Sprintf("vendor = $%d", len(args)))
	}
	if filter.Status != nil {
		if err := filter.Status.Validate(); err != nil {
			return nil, apperror.Wrap(apperror.CodeValidationError, "", err)
		}
		args = append(args, string(*filter.Status))
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	args = append(args, limit, filter.Offset)
	query := `SELECT ` + deviceColumns + ` FROM switches WHERE ` + strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY created_at, id LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := r.q.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDatabaseError(err, "", "list devices")
	}
	defer rows.Close()
	result := make([]device.Device, 0)
	for rows.Next() {
		value, scanErr := scanDevice(rows)
		if scanErr != nil {
			return nil, mapDatabaseError(scanErr, "", "scan device")
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(err, "", "iterate devices")
	}
	return result, nil
}

// Update replaces mutable switch fields for an active switch.
func (r *DeviceRepository) Update(ctx context.Context, value device.Device) (device.Device, error) {
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = time.Now().UTC()
	}
	if err := value.Validate(); err != nil {
		return device.Device{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		UPDATE switches SET
			name=$2, host=$3, ssh_port=$4, credential_id=$5::uuid,
			vendor=$6, model=$7, os_version=$8, detect_mode=$9,
			identity_status=$10, status=$11, last_connected_at=$12,
			last_detected_at=$13, updated_at=$14
		WHERE id=$1::uuid AND deleted_at IS NULL
		RETURNING `+deviceColumns,
		value.ID, value.Name, value.Host, value.SSHPort, value.CredentialID,
		string(value.Vendor), value.Model, value.OSVersion, string(value.DetectMode),
		string(value.IdentityStatus), string(value.Status), value.LastConnectedAt,
		value.LastDetectedAt, value.UpdatedAt,
	)
	result, err := scanDevice(row)
	return result, mapDatabaseError(err, apperror.CodeDeviceNotFound, "update device")
}

// SoftDelete marks an active switch deleted while preserving history.
func (r *DeviceRepository) SoftDelete(ctx context.Context, id string) error {
	row := r.q.QueryRow(ctx, `
		UPDATE switches SET deleted_at=now(), updated_at=now()
		WHERE id=$1::uuid AND deleted_at IS NULL
		RETURNING id::text`, id)
	var returned string
	return mapDatabaseError(row.Scan(&returned), apperror.CodeDeviceNotFound, "delete device")
}

type rowScanner interface{ Scan(...any) error }

func scanDevice(row rowScanner) (device.Device, error) {
	var result device.Device
	var vendor, detectMode, identityStatus, status string
	var lastConnected, lastDetected sql.NullTime
	err := row.Scan(
		&result.ID, &result.Name, &result.Host, &result.SSHPort,
		&result.CredentialID, &vendor, &result.Model, &result.OSVersion,
		&detectMode, &identityStatus, &status, &lastConnected, &lastDetected,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return device.Device{}, err
	}
	result.Vendor = device.Vendor(vendor)
	result.DetectMode = device.DetectMode(detectMode)
	result.IdentityStatus = device.IdentityStatus(identityStatus)
	result.Status = device.Status(status)
	result.LastConnectedAt = timePointer(lastConnected)
	result.LastDetectedAt = timePointer(lastDetected)
	if err := result.Validate(); err != nil {
		return device.Device{}, fmt.Errorf("invalid device row: %w", err)
	}
	return result, nil
}

var _ device.Repository = (*DeviceRepository)(nil)
