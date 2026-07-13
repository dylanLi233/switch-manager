package inventorysvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

type Service struct {
	devices              device.Repository
	credentials          credential.Repository
	executionCredentials credential.ExecutionRepository
	protector            SecretProtector
	tester               ConnectionTester
	detector             IdentityDetector
	now                  func() time.Time
	ids                  func() (string, error)
}

func New(devices device.Repository, credentials credential.Repository, executionCredentials credential.ExecutionRepository, protector SecretProtector, tester ConnectionTester, detector IdentityDetector) (*Service, error) {
	if devices == nil || credentials == nil || executionCredentials == nil {
		return nil, errors.New("device and credential repositories are required")
	}
	if protector == nil {
		return nil, errors.New("credential secret protector is required")
	}
	if tester == nil {
		tester = unavailableTester{}
	}
	if detector == nil {
		detector = unavailableDetector{}
	}
	return &Service{devices: devices, credentials: credentials, executionCredentials: executionCredentials, protector: protector, tester: tester, detector: detector, now: time.Now, ids: randomUUID}, nil
}

func (s *Service) CreateCredential(ctx context.Context, input CredentialInput) (CredentialView, error) {
	if ctx == nil {
		return CredentialView{}, errors.New("context is required")
	}
	id, err := s.newID()
	if err != nil {
		return CredentialView{}, err
	}
	now := s.now().UTC()
	value := credential.Credential{ID: id, Name: strings.TrimSpace(input.Name), Type: input.Type, Username: strings.TrimSpace(input.Username), KeyVersion: s.protector.KeyVersion(), CreatedAt: now, UpdatedAt: now}
	if err := s.applyNewCredentialMaterial(&value, input.Password, input.PrivateKey, input.Passphrase); err != nil {
		return CredentialView{}, err
	}
	metadata, err := s.credentials.Create(ctx, value)
	if err != nil {
		return CredentialView{}, err
	}
	return credentialView(metadata), nil
}

func (s *Service) GetCredential(ctx context.Context, id string) (CredentialView, error) {
	metadata, err := s.credentials.GetMetadata(ctx, strings.TrimSpace(id))
	if err != nil {
		return CredentialView{}, err
	}
	return credentialView(metadata), nil
}

func (s *Service) ListCredentials(ctx context.Context, limit, offset int) ([]CredentialView, error) {
	values, err := s.credentials.ListMetadata(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]CredentialView, len(values))
	for i, value := range values {
		result[i] = credentialView(value)
	}
	return result, nil
}

func (s *Service) UpdateCredential(ctx context.Context, id string, patch CredentialPatch) (CredentialView, error) {
	current, err := s.executionCredentials.GetForExecution(ctx, strings.TrimSpace(id))
	if err != nil {
		return CredentialView{}, err
	}
	if patch.Name != nil {
		current.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Type != nil {
		current.Type = *patch.Type
	}
	if patch.Username != nil {
		current.Username = strings.TrimSpace(*patch.Username)
	}
	current.UpdatedAt = s.now().UTC()
	if err := s.applyPatchedCredentialMaterial(&current, patch); err != nil {
		return CredentialView{}, err
	}
	metadata, err := s.credentials.Update(ctx, current)
	if err != nil {
		return CredentialView{}, err
	}
	return credentialView(metadata), nil
}

func (s *Service) DeleteCredential(ctx context.Context, id string) error {
	return s.credentials.SoftDelete(ctx, strings.TrimSpace(id))
}

func (s *Service) CreateDevice(ctx context.Context, input DeviceInput) (DeviceView, error) {
	if _, err := s.credentials.GetMetadata(ctx, strings.TrimSpace(input.CredentialID)); err != nil {
		return DeviceView{}, err
	}
	id, err := s.newID()
	if err != nil {
		return DeviceView{}, err
	}
	if input.SSHPort == 0 {
		input.SSHPort = 22
	}
	if input.DetectMode == "" {
		input.DetectMode = device.DetectModeAuto
	}
	if input.Status == "" {
		input.Status = device.StatusActive
	}
	now := s.now().UTC()
	value := device.Device{ID: id, Name: strings.TrimSpace(input.Name), Host: strings.TrimSpace(input.Host), SSHPort: input.SSHPort, CredentialID: strings.TrimSpace(input.CredentialID), Vendor: input.Vendor, Model: strings.TrimSpace(input.Model), OSVersion: strings.TrimSpace(input.OSVersion), DetectMode: input.DetectMode, IdentityStatus: device.IdentityUnknown, Status: input.Status, CreatedAt: now, UpdatedAt: now}
	created, err := s.devices.Create(ctx, value)
	if err != nil {
		return DeviceView{}, err
	}
	return s.deviceView(ctx, created)
}

func (s *Service) GetDevice(ctx context.Context, id string) (DeviceView, error) {
	value, err := s.devices.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return DeviceView{}, err
	}
	return s.deviceView(ctx, value)
}

func (s *Service) ListDevices(ctx context.Context, filter device.ListFilter) ([]DeviceView, error) {
	values, err := s.devices.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	result := make([]DeviceView, 0, len(values))
	for _, value := range values {
		view, viewErr := s.deviceView(ctx, value)
		if viewErr != nil {
			return nil, viewErr
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Service) UpdateDevice(ctx context.Context, id string, patch DevicePatch) (DeviceView, error) {
	current, err := s.devices.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return DeviceView{}, err
	}
	identityChanged := false
	if patch.Name != nil {
		current.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Host != nil {
		current.Host = strings.TrimSpace(*patch.Host)
		identityChanged = true
	}
	if patch.SSHPort != nil {
		current.SSHPort = *patch.SSHPort
		identityChanged = true
	}
	if patch.CredentialID != nil {
		if _, err := s.credentials.GetMetadata(ctx, strings.TrimSpace(*patch.CredentialID)); err != nil {
			return DeviceView{}, err
		}
		current.CredentialID = strings.TrimSpace(*patch.CredentialID)
	}
	if patch.Vendor != nil {
		current.Vendor = *patch.Vendor
		identityChanged = true
	}
	if patch.Model != nil {
		current.Model = strings.TrimSpace(*patch.Model)
		identityChanged = true
	}
	if patch.OSVersion != nil {
		current.OSVersion = strings.TrimSpace(*patch.OSVersion)
		identityChanged = true
	}
	if patch.DetectMode != nil {
		current.DetectMode = *patch.DetectMode
		identityChanged = true
	}
	if patch.Status != nil {
		current.Status = *patch.Status
	}
	if identityChanged {
		current.IdentityStatus = device.IdentityUnknown
		current.LastDetectedAt = nil
	}
	current.UpdatedAt = s.now().UTC()
	updated, err := s.devices.Update(ctx, current)
	if err != nil {
		return DeviceView{}, err
	}
	return s.deviceView(ctx, updated)
}

func (s *Service) DeleteDevice(ctx context.Context, id string) error {
	return s.devices.SoftDelete(ctx, strings.TrimSpace(id))
}

func (s *Service) TestConnection(ctx context.Context, id string) (ConnectionTestResult, error) {
	value, material, err := s.loadExecutionInputs(ctx, id)
	if err != nil {
		return ConnectionTestResult{}, err
	}
	defer material.Clear()
	result, testErr := s.tester.Test(ctx, value, material)
	now := s.now().UTC()
	value.UpdatedAt = now
	if testErr != nil {
		value.Status = device.StatusUnreachable
		_, _ = s.devices.Update(context.WithoutCancel(ctx), value)
		return ConnectionTestResult{}, testErr
	}
	value.Status = device.StatusActive
	value.LastConnectedAt = &now
	if _, err := s.devices.Update(ctx, value); err != nil {
		return ConnectionTestResult{}, err
	}
	return result, nil
}

func (s *Service) Detect(ctx context.Context, id string) (DetectionView, error) {
	value, material, err := s.loadExecutionInputs(ctx, id)
	if err != nil {
		return DetectionView{}, err
	}
	defer material.Clear()
	observed, err := s.detector.Detect(ctx, value, material)
	if err != nil {
		return DetectionView{}, err
	}
	if err := observed.Vendor.Validate(); err != nil {
		return DetectionView{}, apperror.Wrap(apperror.CodeCommandOutputUnparsable, "", err)
	}
	now := s.now().UTC()
	value.Model = strings.TrimSpace(observed.Model)
	value.OSVersion = strings.TrimSpace(observed.OSVersion)
	value.LastDetectedAt = &now
	value.UpdatedAt = now
	if observed.Vendor != value.Vendor {
		value.IdentityStatus = device.IdentityMismatch
	} else {
		value.IdentityStatus = device.IdentityVerified
	}
	updated, err := s.devices.Update(ctx, value)
	if err != nil {
		return DetectionView{}, err
	}
	view, err := s.deviceView(ctx, updated)
	if err != nil {
		return DetectionView{}, err
	}
	observed.Capabilities = append([]string(nil), observed.Capabilities...)
	return DetectionView{Device: view, Result: observed}, nil
}

func (s *Service) loadExecutionInputs(ctx context.Context, id string) (device.Device, AuthenticationMaterial, error) {
	value, err := s.devices.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return device.Device{}, AuthenticationMaterial{}, err
	}
	stored, err := s.executionCredentials.GetForExecution(ctx, value.CredentialID)
	if err != nil {
		return device.Device{}, AuthenticationMaterial{}, err
	}
	material, err := s.decryptCredential(stored)
	if err != nil {
		return device.Device{}, AuthenticationMaterial{}, err
	}
	return value, material, nil
}

func (s *Service) decryptCredential(stored credential.Credential) (AuthenticationMaterial, error) {
	if stored.KeyVersion != s.protector.KeyVersion() {
		return AuthenticationMaterial{}, apperror.New(apperror.CodeInternalError, "")
	}
	result := AuthenticationMaterial{Type: stored.Type, Username: stored.Username}
	var err error
	switch stored.Type {
	case credential.TypePassword:
		result.Password, err = s.protector.Decrypt(stored.EncryptedSecret)
	case credential.TypeSSHPrivateKey:
		result.PrivateKey, err = s.protector.Decrypt(stored.EncryptedPrivateKey)
		if err == nil && len(stored.EncryptedPassphrase) > 0 {
			result.Passphrase, err = s.protector.Decrypt(stored.EncryptedPassphrase)
		}
	default:
		err = stored.Type.Validate()
	}
	if err != nil {
		result.Clear()
		return AuthenticationMaterial{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return result, nil
}

func (s *Service) applyNewCredentialMaterial(value *credential.Credential, password, privateKey, passphrase string) error {
	switch value.Type {
	case credential.TypePassword:
		if password == "" {
			return apperror.New(apperror.CodeValidationError, "")
		}
		encrypted, err := s.protector.Encrypt([]byte(password))
		if err != nil {
			return apperror.Wrap(apperror.CodeInternalError, "", err)
		}
		value.EncryptedSecret = encrypted
	case credential.TypeSSHPrivateKey:
		if strings.TrimSpace(privateKey) == "" {
			return apperror.New(apperror.CodeValidationError, "")
		}
		encrypted, err := s.protector.Encrypt([]byte(privateKey))
		if err != nil {
			return apperror.Wrap(apperror.CodeInternalError, "", err)
		}
		value.EncryptedPrivateKey = encrypted
		if passphrase != "" {
			value.EncryptedPassphrase, err = s.protector.Encrypt([]byte(passphrase))
			if err != nil {
				return apperror.Wrap(apperror.CodeInternalError, "", err)
			}
		}
	default:
		return apperror.Wrap(apperror.CodeValidationError, "", value.Type.Validate())
	}
	return nil
}

func (s *Service) applyPatchedCredentialMaterial(value *credential.Credential, patch CredentialPatch) error {
	switch value.Type {
	case credential.TypePassword:
		if patch.Password != nil {
			if *patch.Password == "" {
				return apperror.New(apperror.CodeValidationError, "")
			}
			encrypted, err := s.protector.Encrypt([]byte(*patch.Password))
			if err != nil {
				return apperror.Wrap(apperror.CodeInternalError, "", err)
			}
			value.EncryptedSecret = encrypted
		}
		value.EncryptedPrivateKey, value.EncryptedPassphrase = nil, nil
		if len(value.EncryptedSecret) == 0 {
			return apperror.New(apperror.CodeValidationError, "")
		}
	case credential.TypeSSHPrivateKey:
		if patch.PrivateKey != nil {
			if strings.TrimSpace(*patch.PrivateKey) == "" {
				return apperror.New(apperror.CodeValidationError, "")
			}
			encrypted, err := s.protector.Encrypt([]byte(*patch.PrivateKey))
			if err != nil {
				return apperror.Wrap(apperror.CodeInternalError, "", err)
			}
			value.EncryptedPrivateKey = encrypted
		}
		if patch.Passphrase != nil {
			if *patch.Passphrase == "" {
				value.EncryptedPassphrase = nil
			} else {
				encrypted, err := s.protector.Encrypt([]byte(*patch.Passphrase))
				if err != nil {
					return apperror.Wrap(apperror.CodeInternalError, "", err)
				}
				value.EncryptedPassphrase = encrypted
			}
		}
		value.EncryptedSecret = nil
		if len(value.EncryptedPrivateKey) == 0 {
			return apperror.New(apperror.CodeValidationError, "")
		}
	default:
		return apperror.Wrap(apperror.CodeValidationError, "", value.Type.Validate())
	}
	value.KeyVersion = s.protector.KeyVersion()
	return nil
}

func (s *Service) deviceView(ctx context.Context, value device.Device) (DeviceView, error) {
	metadata, err := s.credentials.GetMetadata(ctx, value.CredentialID)
	if err != nil {
		return DeviceView{}, err
	}
	return DeviceView{ID: value.ID, Name: value.Name, Host: value.Host, SSHPort: value.SSHPort, Credential: CredentialRef{ID: metadata.ID, Name: metadata.Name}, Vendor: value.Vendor, Model: value.Model, OSVersion: value.OSVersion, DetectMode: value.DetectMode, IdentityStatus: value.IdentityStatus, Status: value.Status, LastConnectedAt: value.LastConnectedAt, LastDetectedAt: value.LastDetectedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}, nil
}

func credentialView(value credential.Metadata) CredentialView {
	return CredentialView{ID: value.ID, Name: value.Name, Type: value.Type, Username: value.Username, KeyVersion: value.KeyVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func (s *Service) newID() (string, error) {
	id, err := s.ids()
	if err != nil || strings.TrimSpace(id) == "" {
		return "", appError(err)
	}
	return id, nil
}

func appError(err error) error {
	if err == nil {
		err = errors.New("empty generated ID")
	}
	return apperror.Wrap(apperror.CodeInternalError, "", err)
}

func randomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded), nil
}

type unavailableTester struct{}

func (unavailableTester) Test(context.Context, device.Device, AuthenticationMaterial) (ConnectionTestResult, error) {
	return ConnectionTestResult{}, apperror.Wrap(apperror.CodeOperationNotImplemented, "", errFrameworkUnavailable)
}

type unavailableDetector struct{}

func (unavailableDetector) Detect(context.Context, device.Device, AuthenticationMaterial) (DetectionResult, error) {
	return DetectionResult{}, apperror.Wrap(apperror.CodeOperationNotImplemented, "", errFrameworkUnavailable)
}
