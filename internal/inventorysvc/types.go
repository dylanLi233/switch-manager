// Package inventorysvc implements device and credential management use cases.
package inventorysvc

import (
	"context"
	"errors"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

// SecretProtector encrypts plaintext credential material before persistence.
type SecretProtector interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
	KeyVersion() string
}

// AuthenticationMaterial exists only for a single connection or detection call.
type AuthenticationMaterial struct {
	Type       credential.Type
	Username   string
	Password   []byte
	PrivateKey []byte
	Passphrase []byte
}

func (m *AuthenticationMaterial) Clear() {
	if m == nil {
		return
	}
	clear(m.Password)
	clear(m.PrivateKey)
	clear(m.Passphrase)
}

type ConnectionTestResult struct {
	TCPConnected   bool          `json:"tcp_connected"`
	SSHNegotiated  bool          `json:"ssh_negotiated"`
	Authenticated  bool          `json:"authenticated"`
	PromptDetected bool          `json:"prompt_detected"`
	PromptFamily   string        `json:"prompt_family,omitempty"`
	ServerVersion  string        `json:"server_version,omitempty"`
	Latency        time.Duration `json:"latency"`
}

type ConnectionTester interface {
	Test(context.Context, device.Device, AuthenticationMaterial) (ConnectionTestResult, error)
}

type DetectionResult struct {
	Vendor          device.Vendor `json:"vendor"`
	Model           string        `json:"model"`
	OSVersion       string        `json:"os_version"`
	EvidenceSummary string        `json:"evidence_summary"`
	Capabilities    []string      `json:"capabilities"`
}

type IdentityDetector interface {
	Detect(context.Context, device.Device, AuthenticationMaterial) (DetectionResult, error)
}

type CredentialInput struct {
	Name       string
	Type       credential.Type
	Username   string
	Password   string
	PrivateKey string
	Passphrase string
}

type CredentialPatch struct {
	Name       *string
	Type       *credential.Type
	Username   *string
	Password   *string
	PrivateKey *string
	Passphrase *string
}

type CredentialView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       credential.Type `json:"type"`
	Username   string          `json:"username"`
	KeyVersion string          `json:"key_version"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type DeviceInput struct {
	Name         string
	Host         string
	SSHPort      int
	CredentialID string
	Vendor       device.Vendor
	Model        string
	OSVersion    string
	DetectMode   device.DetectMode
	Status       device.Status
}

type DevicePatch struct {
	Name         *string
	Host         *string
	SSHPort      *int
	CredentialID *string
	Vendor       *device.Vendor
	Model        *string
	OSVersion    *string
	DetectMode   *device.DetectMode
	Status       *device.Status
}

type CredentialRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type DeviceView struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Host            string                `json:"host"`
	SSHPort         int                   `json:"ssh_port"`
	Credential      CredentialRef         `json:"credential"`
	Vendor          device.Vendor         `json:"vendor"`
	Model           string                `json:"model"`
	OSVersion       string                `json:"os_version"`
	DetectMode      device.DetectMode     `json:"detect_mode"`
	IdentityStatus  device.IdentityStatus `json:"identity_status"`
	Status          device.Status         `json:"status"`
	LastConnectedAt *time.Time            `json:"last_connected_at,omitempty"`
	LastDetectedAt  *time.Time            `json:"last_detected_at,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type DetectionView struct {
	Device DeviceView      `json:"device"`
	Result DetectionResult `json:"result"`
}

var errFrameworkUnavailable = errors.New("connection or identity framework is not configured")
