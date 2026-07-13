package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
)

type InventoryService interface {
	CreateCredential(context.Context, inventorysvc.CredentialInput) (inventorysvc.CredentialView, error)
	GetCredential(context.Context, string) (inventorysvc.CredentialView, error)
	ListCredentials(context.Context, int, int) ([]inventorysvc.CredentialView, error)
	UpdateCredential(context.Context, string, inventorysvc.CredentialPatch) (inventorysvc.CredentialView, error)
	DeleteCredential(context.Context, string) error
	CreateDevice(context.Context, inventorysvc.DeviceInput) (inventorysvc.DeviceView, error)
	GetDevice(context.Context, string) (inventorysvc.DeviceView, error)
	ListDevices(context.Context, device.ListFilter) ([]inventorysvc.DeviceView, error)
	UpdateDevice(context.Context, string, inventorysvc.DevicePatch) (inventorysvc.DeviceView, error)
	DeleteDevice(context.Context, string) error
	TestConnection(context.Context, string) (inventorysvc.ConnectionTestResult, error)
	Detect(context.Context, string) (inventorysvc.DetectionView, error)
}

type InventoryHandlers struct{ service InventoryService }

func NewInventoryHandlers(service InventoryService) (*InventoryHandlers, error) {
	if service == nil {
		return nil, errors.New("inventory service is required")
	}
	return &InventoryHandlers{service: service}, nil
}

func (h *InventoryHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.service == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, permission auth.Permission, scope ScopeResolver, handler ErrorHandlerFunc) {
		wrapped := AuthenticationMiddleware(authenticator)(RequirePermission(permission, scope)(AdaptErrorHandler(handler)))
		mux.Handle(pattern, wrapped)
	}
	register("POST /api/v1/credentials", auth.PermissionCredentialManage, globalScope, h.createCredential)
	register("GET /api/v1/credentials", auth.PermissionCredentialManage, globalScope, h.listCredentials)
	register("GET /api/v1/credentials/{credentialID}", auth.PermissionCredentialManage, globalScope, h.getCredential)
	register("PATCH /api/v1/credentials/{credentialID}", auth.PermissionCredentialManage, globalScope, h.updateCredential)
	register("DELETE /api/v1/credentials/{credentialID}", auth.PermissionCredentialManage, globalScope, h.deleteCredential)
	register("POST /api/v1/switches", auth.PermissionDeviceManage, globalScope, h.createDevice)
	register("GET /api/v1/switches", auth.PermissionDeviceRead, globalScope, h.listDevices)
	register("GET /api/v1/switches/{switchID}", auth.PermissionDeviceRead, switchScope, h.getDevice)
	register("PATCH /api/v1/switches/{switchID}", auth.PermissionDeviceManage, switchScope, h.updateDevice)
	register("DELETE /api/v1/switches/{switchID}", auth.PermissionDeviceManage, switchScope, h.deleteDevice)
	register("POST /api/v1/switches/{switchID}/test-connection", auth.PermissionDeviceManage, switchScope, h.testConnection)
	register("POST /api/v1/switches/{switchID}/detect", auth.PermissionDeviceManage, switchScope, h.detectDevice)
}

func globalScope(*http.Request) (auth.Scope, error) { return auth.Scope{Type: auth.ScopeGlobal}, nil }
func switchScope(r *http.Request) (auth.Scope, error) {
	id := strings.TrimSpace(r.PathValue("switchID"))
	if id == "" {
		return auth.Scope{}, apperror.New(apperror.CodeValidationError, "")
	}
	return auth.Scope{Type: auth.ScopeSpecificResource, ID: id}, nil
}

func (h *InventoryHandlers) createCredential(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name       string          `json:"name"`
		Type       credential.Type `json:"type"`
		Username   string          `json:"username"`
		Password   string          `json:"password"`
		PrivateKey string          `json:"private_key"`
		Passphrase string          `json:"passphrase"`
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	value, err := h.service.CreateCredential(r.Context(), inventorysvc.CredentialInput{Name: body.Name, Type: body.Type, Username: body.Username, Password: body.Password, PrivateKey: body.PrivateKey, Passphrase: body.Passphrase})
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusCreated, value)
	return nil
}

func (h *InventoryHandlers) getCredential(w http.ResponseWriter, r *http.Request) error {
	value, err := h.service.GetCredential(r.Context(), r.PathValue("credentialID"))
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func (h *InventoryHandlers) listCredentials(w http.ResponseWriter, r *http.Request) error {
	limit, offset, err := pagination(r)
	if err != nil {
		return err
	}
	values, err := h.service.ListCredentials(r.Context(), limit, offset)
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, map[string]any{"items": values, "page": offset/limit + 1, "page_size": limit})
	return nil
}

func (h *InventoryHandlers) updateCredential(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name       *string          `json:"name"`
		Type       *credential.Type `json:"type"`
		Username   *string          `json:"username"`
		Password   *string          `json:"password"`
		PrivateKey *string          `json:"private_key"`
		Passphrase *string          `json:"passphrase"`
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	value, err := h.service.UpdateCredential(r.Context(), r.PathValue("credentialID"), inventorysvc.CredentialPatch{Name: body.Name, Type: body.Type, Username: body.Username, Password: body.Password, PrivateKey: body.PrivateKey, Passphrase: body.Passphrase})
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func (h *InventoryHandlers) deleteCredential(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.DeleteCredential(r.Context(), r.PathValue("credentialID")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *InventoryHandlers) createDevice(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name         string            `json:"name"`
		Host         string            `json:"host"`
		SSHPort      int               `json:"ssh_port"`
		CredentialID string            `json:"credential_id"`
		Vendor       device.Vendor     `json:"vendor"`
		Model        string            `json:"model"`
		OSVersion    string            `json:"os_version"`
		DetectMode   device.DetectMode `json:"detect_mode"`
		Status       device.Status     `json:"status"`
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	value, err := h.service.CreateDevice(r.Context(), inventorysvc.DeviceInput{Name: body.Name, Host: body.Host, SSHPort: body.SSHPort, CredentialID: body.CredentialID, Vendor: body.Vendor, Model: body.Model, OSVersion: body.OSVersion, DetectMode: body.DetectMode, Status: body.Status})
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusCreated, value)
	return nil
}

func (h *InventoryHandlers) getDevice(w http.ResponseWriter, r *http.Request) error {
	value, err := h.service.GetDevice(r.Context(), r.PathValue("switchID"))
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func (h *InventoryHandlers) listDevices(w http.ResponseWriter, r *http.Request) error {
	limit, offset, err := pagination(r)
	if err != nil {
		return err
	}
	filter := device.ListFilter{Limit: limit, Offset: offset, Keyword: r.URL.Query().Get("keyword")}
	if raw := strings.TrimSpace(r.URL.Query().Get("vendor")); raw != "" {
		vendor := device.Vendor(strings.ToUpper(raw))
		filter.Vendor = &vendor
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := device.Status(strings.ToUpper(raw))
		filter.Status = &status
	}
	values, err := h.service.ListDevices(r.Context(), filter)
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, map[string]any{"items": values, "page": offset/limit + 1, "page_size": limit})
	return nil
}

func (h *InventoryHandlers) updateDevice(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name         *string            `json:"name"`
		Host         *string            `json:"host"`
		SSHPort      *int               `json:"ssh_port"`
		CredentialID *string            `json:"credential_id"`
		Vendor       *device.Vendor     `json:"vendor"`
		Model        *string            `json:"model"`
		OSVersion    *string            `json:"os_version"`
		DetectMode   *device.DetectMode `json:"detect_mode"`
		Status       *device.Status     `json:"status"`
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	value, err := h.service.UpdateDevice(r.Context(), r.PathValue("switchID"), inventorysvc.DevicePatch{Name: body.Name, Host: body.Host, SSHPort: body.SSHPort, CredentialID: body.CredentialID, Vendor: body.Vendor, Model: body.Model, OSVersion: body.OSVersion, DetectMode: body.DetectMode, Status: body.Status})
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func (h *InventoryHandlers) deleteDevice(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.DeleteDevice(r.Context(), r.PathValue("switchID")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *InventoryHandlers) testConnection(w http.ResponseWriter, r *http.Request) error {
	value, err := h.service.TestConnection(r.Context(), r.PathValue("switchID"))
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func (h *InventoryHandlers) detectDevice(w http.ResponseWriter, r *http.Request) error {
	value, err := h.service.Detect(r.Context(), r.PathValue("switchID"))
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, value)
	return nil
}

func decodeStrictJSON(r *http.Request, destination any) error {
	if r == nil || r.Body == nil {
		return apperror.New(apperror.CodeValidationError, "")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return apperror.New(apperror.CodeValidationError, "")
	}
	return nil
}

func pagination(r *http.Request) (int, int, error) {
	page := 1
	size := 50
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return 0, 0, apperror.New(apperror.CodeValidationError, "")
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > 500 {
			return 0, 0, apperror.New(apperror.CodeValidationError, "")
		}
	}
	return size, (page - 1) * size, nil
}
