package fake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const maximumPageSize = 500

type tableQueryOptions struct {
	Page        int `json:"page"`
	PageSize    int `json:"page_size"`
	ResultLimit int `json:"result_limit"`
}

func telemetryCommand(request pluginapi.PlanRequest) (string, error) {
	if request.Class != pluginapi.ClassQuery || request.SaveConfig {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "telemetry operations require QUERY class without save_config")
	}
	switch request.Operation {
	case pluginapi.OperationDeviceStatusGet:
		if len(request.Parameters) != 0 {
			return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "device_status.get accepts no parameters")
		}
		return "fake.device_status.get", nil
	case pluginapi.OperationMACTableList, pluginapi.OperationARPTableList:
		options, err := queryOptionsFromParameters(request.Parameters)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(options)
		if err != nil {
			return "", pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "encode fake table query", err)
		}
		prefix := "fake.mac_table.list"
		if request.Operation == pluginapi.OperationARPTableList {
			prefix = "fake.arp_table.list"
		}
		return prefix + " " + string(encoded), nil
	default:
		return "", pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "telemetry operation is not declared")
	}
}

func queryOptionsFromParameters(parameters map[string]any) (tableQueryOptions, error) {
	if len(parameters) != 3 {
		return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "page, page_size, and result_limit are required")
	}
	for key := range parameters {
		if key != "page" && key != "page_size" && key != "result_limit" {
			return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "unknown table query parameter")
		}
	}
	page, err := integerParameter(parameters["page"])
	if err != nil || page < 1 || page > 1_000_000 {
		return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "page must be between 1 and 1000000")
	}
	pageSize, err := integerParameter(parameters["page_size"])
	if err != nil || pageSize < 1 || pageSize > maximumPageSize {
		return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "page_size must be between 1 and 500")
	}
	resultLimit, err := integerParameter(parameters["result_limit"])
	if err != nil || resultLimit < 1 || resultLimit > 100_000 {
		return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "result_limit must be between 1 and 100000")
	}
	return tableQueryOptions{Page: page, PageSize: pageSize, ResultLimit: resultLimit}, nil
}

type telemetryParseOutcome struct {
	Data      any
	ErrorCode string
}

func parseTelemetryOutput(plan pluginapi.ExecutionPlan, record pluginapi.CommandRecord) (telemetryParseOutcome, error) {
	if record.OutputTruncated {
		return telemetryParseOutcome{}, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "telemetry output was truncated")
	}
	switch plan.Operation {
	case pluginapi.OperationMACTableList:
		options, err := queryOptionsFromCommand(record.Command, "fake.mac_table.list")
		if err != nil {
			return telemetryParseOutcome{}, err
		}
		entries, err := decodeMACEntries(record.Output)
		if err != nil {
			return telemetryParseOutcome{}, err
		}
		if len(entries) > options.ResultLimit {
			return telemetryParseOutcome{ErrorCode: string(pluginapi.ErrorResultTooLarge)}, nil
		}
		start, end := pageBounds(options.Page, options.PageSize, len(entries))
		return telemetryParseOutcome{Data: telemetry.MACPage{Entries: append([]telemetry.MACEntry(nil), entries[start:end]...), Page: options.Page, PageSize: options.PageSize, Total: len(entries)}}, nil
	case pluginapi.OperationARPTableList:
		options, err := queryOptionsFromCommand(record.Command, "fake.arp_table.list")
		if err != nil {
			return telemetryParseOutcome{}, err
		}
		entries, err := decodeARPEntries(record.Output)
		if err != nil {
			return telemetryParseOutcome{}, err
		}
		if len(entries) > options.ResultLimit {
			return telemetryParseOutcome{ErrorCode: string(pluginapi.ErrorResultTooLarge)}, nil
		}
		start, end := pageBounds(options.Page, options.PageSize, len(entries))
		return telemetryParseOutcome{Data: telemetry.ARPPage{Entries: append([]telemetry.ARPEntry(nil), entries[start:end]...), Page: options.Page, PageSize: options.PageSize, Total: len(entries)}}, nil
	case pluginapi.OperationDeviceStatusGet:
		status, err := decodeDeviceStatus(record.Output)
		if err != nil {
			return telemetryParseOutcome{}, err
		}
		return telemetryParseOutcome{Data: status}, nil
	default:
		return telemetryParseOutcome{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "telemetry operation is not declared")
	}
}

func queryOptionsFromCommand(command, prefix string) (tableQueryOptions, error) {
	encoded, ok := strings.CutPrefix(command, prefix+" ")
	if !ok {
		return tableQueryOptions{}, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "telemetry command does not match its operation")
	}
	var options tableQueryOptions
	if err := decodeStrictJSON(encoded, &options); err != nil {
		return tableQueryOptions{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "telemetry query options are invalid", err)
	}
	validated, err := queryOptionsFromParameters(map[string]any{"page": options.Page, "page_size": options.PageSize, "result_limit": options.ResultLimit})
	if err != nil {
		return tableQueryOptions{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "telemetry query options are invalid", err)
	}
	return validated, nil
}

func decodeMACEntries(output string) ([]telemetry.MACEntry, error) {
	var wire struct {
		Entries *[]telemetry.MACEntry `json:"entries"`
	}
	if err := decodeStrictJSON(output, &wire); err != nil || wire.Entries == nil {
		return nil, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "MAC table output is incomplete", err)
	}
	entries := make([]telemetry.MACEntry, len(*wire.Entries))
	seen := make(map[string]struct{}, len(entries))
	for index, value := range *wire.Entries {
		normalized, err := telemetry.NormalizeMACEntry(value)
		if err != nil {
			return nil, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, fmt.Sprintf("MAC entry %d is invalid", index), err)
		}
		key := fmt.Sprintf("%d|%s|%s", normalized.VLANID, normalized.MACAddress, normalized.InterfaceName)
		if _, exists := seen[key]; exists {
			return nil, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "MAC table contains a duplicate entry")
		}
		seen[key] = struct{}{}
		entries[index] = normalized
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].VLANID != entries[j].VLANID {
			return entries[i].VLANID < entries[j].VLANID
		}
		if entries[i].MACAddress != entries[j].MACAddress {
			return entries[i].MACAddress < entries[j].MACAddress
		}
		return entries[i].InterfaceName < entries[j].InterfaceName
	})
	return entries, nil
}

func decodeARPEntries(output string) ([]telemetry.ARPEntry, error) {
	var wire struct {
		Entries *[]telemetry.ARPEntry `json:"entries"`
	}
	if err := decodeStrictJSON(output, &wire); err != nil || wire.Entries == nil {
		return nil, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "ARP table output is incomplete", err)
	}
	entries := make([]telemetry.ARPEntry, len(*wire.Entries))
	seen := make(map[string]struct{}, len(entries))
	for index, value := range *wire.Entries {
		normalized, err := telemetry.NormalizeARPEntry(value)
		if err != nil {
			return nil, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, fmt.Sprintf("ARP entry %d is invalid", index), err)
		}
		key := normalized.IPAddress + "|" + normalized.InterfaceName
		if _, exists := seen[key]; exists {
			return nil, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "ARP table contains a duplicate entry")
		}
		seen[key] = struct{}{}
		entries[index] = normalized
	}
	sort.Slice(entries, func(i, j int) bool {
		left, _ := netip.ParseAddr(entries[i].IPAddress)
		right, _ := netip.ParseAddr(entries[j].IPAddress)
		if comparison := left.Compare(right); comparison != 0 {
			return comparison < 0
		}
		return entries[i].InterfaceName < entries[j].InterfaceName
	})
	return entries, nil
}

func decodeDeviceStatus(output string) (telemetry.DeviceStatus, error) {
	var wire struct {
		Hostname           *string                `json:"hostname"`
		UptimeSeconds      *int64                 `json:"uptime_seconds"`
		HealthState        *telemetry.HealthState `json:"health_state"`
		CPUUsagePercent    *float64               `json:"cpu_usage_percent"`
		MemoryUsagePercent *float64               `json:"memory_usage_percent"`
		TemperatureCelsius *float64               `json:"temperature_celsius"`
		ActiveAlarms       *[]string              `json:"active_alarms"`
		CollectedAt        *json.RawMessage        `json:"collected_at"`
	}
	if err := decodeStrictJSON(output, &wire); err != nil {
		return telemetry.DeviceStatus{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "device status output is invalid", err)
	}
	if wire.Hostname == nil || wire.UptimeSeconds == nil || wire.HealthState == nil || wire.ActiveAlarms == nil || wire.CollectedAt == nil {
		return telemetry.DeviceStatus{}, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "device status output is incomplete")
	}
	var collectedAt jsonTime
	if err := json.Unmarshal(*wire.CollectedAt, &collectedAt); err != nil {
		return telemetry.DeviceStatus{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "device status collected_at is invalid", err)
	}
	value := telemetry.DeviceStatus{Hostname: *wire.Hostname, UptimeSeconds: *wire.UptimeSeconds, HealthState: *wire.HealthState, CPUUsagePercent: wire.CPUUsagePercent, MemoryUsagePercent: wire.MemoryUsagePercent, TemperatureCelsius: wire.TemperatureCelsius, ActiveAlarms: append([]string(nil), (*wire.ActiveAlarms)...), CollectedAt: collectedAt.Time}
	normalized, err := telemetry.NormalizeDeviceStatus(value)
	if err != nil {
		return telemetry.DeviceStatus{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "device status output has an invalid schema", err)
	}
	return normalized, nil
}

type jsonTime struct{ Time time.Time }

func (t *jsonTime) UnmarshalJSON(data []byte) error {
	var value time.Time
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	t.Time = value
	return nil
}

func decodeStrictJSON(encoded string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return err
	}
	return nil
}

func pageBounds(page, pageSize, total int) (int, int) {
	start64 := int64(page-1) * int64(pageSize)
	if start64 >= int64(total) {
		return total, total
	}
	start := int(start64)
	end := start + pageSize
	if end > total {
		end = total
	}
	return start, end
}

func isTelemetryOperation(name pluginapi.OperationName) bool {
	switch name {
	case pluginapi.OperationMACTableList, pluginapi.OperationARPTableList, pluginapi.OperationDeviceStatusGet:
		return true
	default:
		return false
	}
}
