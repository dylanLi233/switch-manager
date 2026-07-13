package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type TelemetryHandlers struct {
	operations  OperationSubmitter
	resultLimit int
}

func NewTelemetryHandlers(operations OperationSubmitter, resultLimit int) (*TelemetryHandlers, error) {
	if operations == nil {
		return nil, errors.New("operation service is required")
	}
	if resultLimit < 1 || resultLimit > 100_000 {
		return nil, errors.New("telemetry result limit must be between 1 and 100000")
	}
	return &TelemetryHandlers{operations: operations, resultLimit: resultLimit}, nil
}

func (h *TelemetryHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.operations == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, handler ErrorHandlerFunc) {
		mux.Handle(pattern, AuthenticationMiddleware(authenticator)(RequirePermission(auth.PermissionOperationQuery, switchScope)(AdaptErrorHandler(handler))))
	}
	register("GET /api/v1/switches/{switchID}/mac-table", h.macTable)
	register("GET /api/v1/switches/{switchID}/arp-table", h.arpTable)
	register("GET /api/v1/switches/{switchID}/status", h.status)

	// Custom commands share the same durable Operation Service and Scheduler as
	// telemetry and other device operations. Their handler performs separate
	// custom_read/custom_config authorization and command-policy preflight.
	(&CustomCommandHandlers{operations: h.operations}).Register(mux, authenticator)
}

func (h *TelemetryHandlers) macTable(w http.ResponseWriter, r *http.Request) error {
	return h.table(w, r, pluginapi.OperationMACTableList)
}

func (h *TelemetryHandlers) arpTable(w http.ResponseWriter, r *http.Request) error {
	return h.table(w, r, pluginapi.OperationARPTableList)
}

func (h *TelemetryHandlers) table(w http.ResponseWriter, r *http.Request, name pluginapi.OperationName) error {
	page, pageSize, err := telemetryPagination(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{
		Name: operation.Name(name), Class: operation.ClassQuery,
		DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync,
		Parameters: map[string]any{"page": page, "page_size": pageSize, "result_limit": h.resultLimit},
	})
}

func (h *TelemetryHandlers) status(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query()) != 0 {
		return validationError(errors.New("status query does not accept query parameters"))
	}
	return h.submitAndWrite(w, r, operation.Request{
		Name: operation.Name(pluginapi.OperationDeviceStatusGet), Class: operation.ClassQuery,
		DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync,
	})
}

func (h *TelemetryHandlers) submitAndWrite(w http.ResponseWriter, r *http.Request, request operation.Request) error {
	actor, err := ActorFromRequest(r)
	if err != nil {
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	request.Actor = actor
	submission, err := h.operations.Submit(r.Context(), operationsvc.SubmitRequest{RequestID: RequestIDFromContext(r.Context()), Operation: request})
	if err != nil {
		return err
	}
	return writeVLANSubmission(w, r, submission, operation.ExecutionModeSync)
}

func telemetryPagination(r *http.Request) (int, int, error) {
	for key := range r.URL.Query() {
		if key != "page" && key != "page_size" {
			return 0, 0, validationError(errors.New("unknown telemetry query parameter"))
		}
	}
	page, pageSize := 1, 50
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1_000_000 {
			return 0, 0, validationError(errors.New("page must be between 1 and 1000000"))
		}
		page = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 500 {
			return 0, 0, validationError(errors.New("page_size must be between 1 and 500"))
		}
		pageSize = value
	}
	return page, pageSize, nil
}
