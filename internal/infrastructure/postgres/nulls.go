package postgres

import (
	"database/sql"
	"time"
)

func timePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func nilIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func bytesOrNil(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
