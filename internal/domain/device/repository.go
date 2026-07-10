package device

import "context"

// ListFilter limits device repository queries.
type ListFilter struct {
	Vendor *Vendor
	Status *Status
	Limit  int
	Offset int
}

// Repository persists active managed switches. Deleted devices are excluded.
type Repository interface {
	Create(context.Context, Device) (Device, error)
	Get(context.Context, string) (Device, error)
	List(context.Context, ListFilter) ([]Device, error)
	Update(context.Context, Device) (Device, error)
	SoftDelete(context.Context, string) error
}
