package inventorysvc

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/secretbox"
)

type memoryCredentials struct {
	mu     sync.Mutex
	values map[string]credential.Credential
}

func newMemoryCredentials() *memoryCredentials {
	return &memoryCredentials{values: map[string]credential.Credential{}}
}
func (r *memoryCredentials) Create(_ context.Context, v credential.Credential) (credential.Metadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[v.ID] = v
	return metadata(v), nil
}
func (r *memoryCredentials) GetMetadata(_ context.Context, id string) (credential.Metadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[id]
	if !ok {
		return credential.Metadata{}, errors.New("not found")
	}
	return metadata(v), nil
}
func (r *memoryCredentials) ListMetadata(context.Context, int, int) ([]credential.Metadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]credential.Metadata, 0, len(r.values))
	for _, v := range r.values {
		out = append(out, metadata(v))
	}
	return out, nil
}
func (r *memoryCredentials) Update(_ context.Context, v credential.Credential) (credential.Metadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[v.ID] = v
	return metadata(v), nil
}
func (r *memoryCredentials) SoftDelete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, id)
	return nil
}
func (r *memoryCredentials) GetForExecution(_ context.Context, id string) (credential.Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[id]
	if !ok {
		return credential.Credential{}, errors.New("not found")
	}
	return v, nil
}
func metadata(v credential.Credential) credential.Metadata {
	return credential.Metadata{ID: v.ID, Name: v.Name, Type: v.Type, Username: v.Username, KeyVersion: v.KeyVersion, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

type memoryDevices struct {
	mu     sync.Mutex
	values map[string]device.Device
}

func newMemoryDevices() *memoryDevices { return &memoryDevices{values: map[string]device.Device{}} }
func (r *memoryDevices) Create(_ context.Context, v device.Device) (device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[v.ID] = v
	return v, nil
}
func (r *memoryDevices) Get(_ context.Context, id string) (device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[id]
	if !ok {
		return device.Device{}, errors.New("not found")
	}
	return v, nil
}
func (r *memoryDevices) List(context.Context, device.ListFilter) ([]device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]device.Device, 0, len(r.values))
	for _, v := range r.values {
		out = append(out, v)
	}
	return out, nil
}
func (r *memoryDevices) Update(_ context.Context, v device.Device) (device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[v.ID] = v
	return v, nil
}
func (r *memoryDevices) SoftDelete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, id)
	return nil
}

type testingProbe struct {
	sawPassword string
	result      ConnectionTestResult
	err         error
}

func (p *testingProbe) Test(_ context.Context, _ device.Device, m AuthenticationMaterial) (ConnectionTestResult, error) {
	p.sawPassword = string(m.Password)
	return p.result, p.err
}

type testingDetector struct {
	result      DetectionResult
	sawPassword string
}

func (d *testingDetector) Detect(_ context.Context, _ device.Device, m AuthenticationMaterial) (DetectionResult, error) {
	d.sawPassword = string(m.Password)
	return d.result, nil
}

func setupService(t *testing.T) (*Service, *memoryCredentials, *memoryDevices, *testingProbe, *testingDetector) {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeyBytes), "v1")
	if err != nil {
		t.Fatal(err)
	}
	credentials := newMemoryCredentials()
	devices := newMemoryDevices()
	probe := &testingProbe{result: ConnectionTestResult{TCPConnected: true, SSHNegotiated: true, Authenticated: true, PromptDetected: true}}
	detector := &testingDetector{result: DetectionResult{Vendor: device.VendorH3C, Model: "S5130", OSVersion: "7.1", EvidenceSummary: "fixture"}}
	service, err := New(devices, credentials, credentials, box, probe, detector)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"credential-id", "device-id"}
	service.ids = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	service.now = func() time.Time { return time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC) }
	return service, credentials, devices, probe, detector
}

func TestCredentialSecretEncryptedAndViewDoesNotExposeIt(t *testing.T) {
	service, credentials, _, _, _ := setupService(t)
	view, err := service.CreateCredential(context.Background(), CredentialInput{Name: "admin", Type: credential.TypePassword, Username: "ops", Password: "plain-password"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "admin" || view.Username != "ops" {
		t.Fatalf("view=%+v", view)
	}
	stored := credentials.values[view.ID]
	if bytes.Contains(stored.EncryptedSecret, []byte("plain-password")) || bytes.Equal(stored.EncryptedSecret, []byte("plain-password")) {
		t.Fatal("plaintext persisted")
	}
	if len(stored.EncryptedSecret) == 0 {
		t.Fatal("ciphertext missing")
	}
}

func TestConnectionUsesDecryptedMaterialAndUpdatesStatus(t *testing.T) {
	service, _, _, probe, _ := setupService(t)
	credentialView, err := service.CreateCredential(context.Background(), CredentialInput{Name: "admin", Type: credential.TypePassword, Username: "ops", Password: "plain-password"})
	if err != nil {
		t.Fatal(err)
	}
	deviceView, err := service.CreateDevice(context.Background(), DeviceInput{Name: "sw", Host: "192.0.2.1", CredentialID: credentialView.ID, Vendor: device.VendorHuawei})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestConnection(context.Background(), deviceView.ID); err != nil {
		t.Fatal(err)
	}
	if probe.sawPassword != "plain-password" {
		t.Fatalf("password=%q", probe.sawPassword)
	}
	updated, err := service.GetDevice(context.Background(), deviceView.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != device.StatusActive || updated.LastConnectedAt == nil {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestDetectionPersistsIdentityMismatch(t *testing.T) {
	service, _, _, _, detector := setupService(t)
	credentialView, _ := service.CreateCredential(context.Background(), CredentialInput{Name: "admin", Type: credential.TypePassword, Username: "ops", Password: "plain-password"})
	deviceView, _ := service.CreateDevice(context.Background(), DeviceInput{Name: "sw", Host: "192.0.2.1", CredentialID: credentialView.ID, Vendor: device.VendorHuawei})
	detected, err := service.Detect(context.Background(), deviceView.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detector.sawPassword != "plain-password" {
		t.Fatal("detector did not receive decrypted credential")
	}
	if detected.Device.IdentityStatus != device.IdentityMismatch || detected.Device.Model != "S5130" {
		t.Fatalf("detected=%+v", detected)
	}
}

func TestIdentityFieldsChangeRequiresRedetection(t *testing.T) {
	service, _, _, _, _ := setupService(t)
	credentialView, _ := service.CreateCredential(context.Background(), CredentialInput{Name: "admin", Type: credential.TypePassword, Username: "ops", Password: "plain-password"})
	deviceView, _ := service.CreateDevice(context.Background(), DeviceInput{Name: "sw", Host: "192.0.2.1", CredentialID: credentialView.ID, Vendor: device.VendorHuawei})
	_, _ = service.Detect(context.Background(), deviceView.ID)
	host := "192.0.2.2"
	updated, err := service.UpdateDevice(context.Background(), deviceView.ID, DevicePatch{Host: &host})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IdentityStatus != device.IdentityUnknown || updated.LastDetectedAt != nil {
		t.Fatalf("updated=%+v", updated)
	}
}
