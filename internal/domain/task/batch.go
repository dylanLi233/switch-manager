package task

import (
	"errors"
	"fmt"
	"strings"
)

const (
	BatchErrorFailed  = "BATCH_FAILED"
	BatchErrorPartial = "BATCH_PARTIAL_SUCCESS"
)

// BatchItem links one stable child sequence to one target device.
type BatchItem struct {
	BatchTaskID string
	DeviceID    string
	ChildTaskID string
	SequenceNo  int
}

func (i BatchItem) Validate() error {
	if strings.TrimSpace(i.BatchTaskID) == "" || strings.TrimSpace(i.DeviceID) == "" || strings.TrimSpace(i.ChildTaskID) == "" {
		return errors.New("batch, device, and child task IDs are required")
	}
	if i.SequenceNo < 1 {
		return errors.New("batch item sequence must be positive")
	}
	return nil
}

// BatchItemState combines the durable link and current child task state.
type BatchItemState struct {
	Item  BatchItem
	Child Persisted
}

func (i BatchItemState) Validate() error {
	if err := i.Item.Validate(); err != nil {
		return err
	}
	if err := i.Child.Validate(); err != nil {
		return err
	}
	if i.Item.ChildTaskID != i.Child.ID || i.Item.DeviceID != i.Child.TargetID || i.Item.BatchTaskID != i.Child.ParentTaskID {
		return errors.New("batch item does not match child task")
	}
	if i.Child.Type != TypeBatchChild {
		return fmt.Errorf("batch child has task type %q", i.Child.Type)
	}
	return nil
}

// BatchSnapshot is the persisted parent aggregate and ordered child states.
type BatchSnapshot struct {
	Batch  Batch
	Parent Persisted
	Items  []BatchItemState
}

func (s BatchSnapshot) Validate() error {
	if err := s.Batch.Validate(); err != nil {
		return err
	}
	if err := s.Parent.Validate(); err != nil {
		return err
	}
	if s.Parent.Type != TypeBatchParent || s.Parent.ID != s.Batch.ParentTaskID || s.Batch.ID != s.Parent.TargetID {
		return errors.New("batch parent task does not match batch aggregate")
	}
	if len(s.Items) != s.Batch.TotalCount {
		return fmt.Errorf("batch item count %d does not match total %d", len(s.Items), s.Batch.TotalCount)
	}
	seenDevice := make(map[string]struct{}, len(s.Items))
	seenChild := make(map[string]struct{}, len(s.Items))
	for index, item := range s.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("batch item %d: %w", index, err)
		}
		if item.Item.BatchTaskID != s.Batch.ID || item.Item.SequenceNo != index+1 {
			return fmt.Errorf("batch item %d is out of order", index)
		}
		if _, exists := seenDevice[item.Item.DeviceID]; exists {
			return errors.New("batch contains duplicate device")
		}
		if _, exists := seenChild[item.Item.ChildTaskID]; exists {
			return errors.New("batch contains duplicate child task")
		}
		seenDevice[item.Item.DeviceID] = struct{}{}
		seenChild[item.Item.ChildTaskID] = struct{}{}
	}
	return nil
}

// RetryableBatchChild reports whether retry-failed may copy the child.
func RetryableBatchChild(status Status) bool {
	return status == StatusFailed || status == StatusInterrupted
}
