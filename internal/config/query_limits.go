package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const QueryResultLimitEnv = "SWITCH_MANAGER_QUERY_RESULT_LIMIT"

const (
	DefaultQueryResultLimit = 5000
	MaximumQueryResultLimit = 100000
)

type QueryLimits struct {
	ResultLimit int
}

func LoadQueryLimits(lookup LookupEnv) (QueryLimits, error) {
	if lookup == nil {
		return QueryLimits{}, errors.New("environment lookup is required")
	}
	limits := QueryLimits{ResultLimit: DefaultQueryResultLimit}
	if raw, ok := lookup(QueryResultLimitEnv); ok && strings.TrimSpace(raw) != "" {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return QueryLimits{}, fmt.Errorf("parse %s: %w", QueryResultLimitEnv, err)
		}
		limits.ResultLimit = value
	}
	if limits.ResultLimit < 1 || limits.ResultLimit > MaximumQueryResultLimit {
		return QueryLimits{}, fmt.Errorf("query result limit must be between 1 and %d", MaximumQueryResultLimit)
	}
	return limits, nil
}
