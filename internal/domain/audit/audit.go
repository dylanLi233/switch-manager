// Package audit defines persisted, redacted audit records.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Record is one append-first audit event. JSON fields must already be redacted.
type Record struct {
	ID                     string
	RequestID              string
	TaskID                 string
	ActorUserID            string
	ActorUsername          string
	ActorRole              string
	ServiceActorID         string
	SourceIP               string
	Action                 string
	TargetType             string
	TargetID               string
	DeviceVendor           string
	DeviceModel            string
	DeviceOSVersion        string
	PluginName             string
	PluginVersion          string
	RequestPayloadRedacted json.RawMessage
	CommandPlanRedacted    json.RawMessage
	ResultSummaryRedacted  json.RawMessage
	Status                 string
	ErrorCode              string
	CreatedAt              time.Time
	FinishedAt             *time.Time
}

// Validate enforces the minimum persistence invariants.
func (r Record) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.RequestID) == "" {
		return errors.New("audit ID and request ID are required")
	}
	if strings.TrimSpace(r.ActorUserID) == "" || strings.TrimSpace(r.ActorUsername) == "" {
		return errors.New("audit actor is required")
	}
	if strings.TrimSpace(r.Action) == "" || strings.TrimSpace(r.TargetType) == "" || strings.TrimSpace(r.TargetID) == "" {
		return errors.New("audit action and target are required")
	}
	if strings.TrimSpace(r.Status) == "" || r.CreatedAt.IsZero() {
		return errors.New("audit status and created time are required")
	}
	if r.FinishedAt != nil && r.FinishedAt.Before(r.CreatedAt) {
		return errors.New("audit finish time cannot precede created time")
	}
	return nil
}

// Repository persists audit records and their final result.
type Repository interface {
	Create(context.Context, Record) (Record, error)
	Get(context.Context, string) (Record, error)
	Complete(context.Context, string, string, string, json.RawMessage, time.Time) (Record, error)
}
