// Package sshprobe provides a strict SSH connection-test implementation.
package sshprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"golang.org/x/crypto/ssh"
)

// PromptProber inspects the initial interactive shell prompt without entering configuration mode.
type PromptProber interface {
	Probe(context.Context, *ssh.Client) (string, error)
}

type Tester struct {
	HostKeyCallback ssh.HostKeyCallback
	Timeout         time.Duration
	Prompt          PromptProber
}

func New(callback ssh.HostKeyCallback, timeout time.Duration, prompt PromptProber) (*Tester, error) {
	if callback == nil {
		return nil, errors.New("SSH host key callback is required")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Tester{HostKeyCallback: WrapHostKeyCallback(callback), Timeout: timeout, Prompt: prompt}, nil
}

func (t *Tester) Test(ctx context.Context, target device.Device, material inventorysvc.AuthenticationMaterial) (inventorysvc.ConnectionTestResult, error) {
	if ctx == nil {
		return inventorysvc.ConnectionTestResult{}, errors.New("context is required")
	}
	if t == nil || t.HostKeyCallback == nil {
		return inventorysvc.ConnectionTestResult{}, apperror.New(apperror.CodeHostKeyVerificationFailed, "")
	}
	auth, err := authMethods(material)
	if err != nil {
		return inventorysvc.ConnectionTestResult{}, err
	}
	address := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.SSHPort))
	started := time.Now()
	dialer := net.Dialer{Timeout: t.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return inventorysvc.ConnectionTestResult{}, apperror.Wrap(apperror.CodeDeviceUnreachable, "", err)
	}
	result := inventorysvc.ConnectionTestResult{TCPConnected: true}
	defer conn.Close()

	deadline := time.Now().Add(t.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)
	config := &ssh.ClientConfig{User: material.Username, Auth: auth, HostKeyCallback: t.HostKeyCallback}
	clientConn, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		var keyErr *knownHostError
		if errors.As(err, &keyErr) {
			return inventorysvc.ConnectionTestResult{}, apperror.Wrap(apperror.CodeHostKeyVerificationFailed, "", err)
		}
		return inventorysvc.ConnectionTestResult{}, apperror.Wrap(apperror.CodeAuthenticationFailed, "", err)
	}
	_ = conn.SetDeadline(time.Time{})
	result.SSHNegotiated = true
	result.Authenticated = true
	result.ServerVersion = string(clientConn.ServerVersion())
	client := ssh.NewClient(clientConn, channels, requests)
	defer client.Close()
	if t.Prompt != nil {
		family, probeErr := t.Prompt.Probe(ctx, client)
		if probeErr != nil {
			return inventorysvc.ConnectionTestResult{}, probeErr
		}
		result.PromptDetected = true
		result.PromptFamily = family
	}
	result.Latency = time.Since(started)
	return result, nil
}

func authMethods(material inventorysvc.AuthenticationMaterial) ([]ssh.AuthMethod, error) {
	switch material.Type {
	case credential.TypePassword:
		if len(material.Password) == 0 {
			return nil, apperror.New(apperror.CodeAuthenticationFailed, "")
		}
		return []ssh.AuthMethod{ssh.Password(string(material.Password))}, nil
	case credential.TypeSSHPrivateKey:
		var signer ssh.Signer
		var err error
		if len(material.Passphrase) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(material.PrivateKey, material.Passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(material.PrivateKey)
		}
		if err != nil {
			return nil, apperror.Wrap(apperror.CodeAuthenticationFailed, "", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		return nil, apperror.Wrap(apperror.CodeAuthenticationFailed, "", material.Type.Validate())
	}
}

// RegexPromptProber requests a PTY and shell, then waits for a caller-supplied prompt pattern.
type RegexPromptProber struct {
	Pattern  *regexp.Regexp
	Family   string
	Timeout  time.Duration
	MaxBytes int
}

func (p RegexPromptProber) Probe(ctx context.Context, client *ssh.Client) (string, error) {
	if client == nil || p.Pattern == nil {
		return "", apperror.New(apperror.CodeCommandOutputUnparsable, "")
	}
	if p.Timeout <= 0 {
		p.Timeout = 5 * time.Second
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 64 * 1024
	}
	session, err := client.NewSession()
	if err != nil {
		return "", apperror.Wrap(apperror.CodeSSHTimeout, "", err)
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", apperror.Wrap(apperror.CodeCommandOutputUnparsable, "", err)
	}
	if err := session.RequestPty("vt100", 24, 80, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	if err := session.Shell(); err != nil {
		return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	result := make(chan error, 1)
	var buffer bytes.Buffer
	var mu sync.Mutex
	go func() {
		chunk := make([]byte, 1024)
		for {
			n, readErr := stdout.Read(chunk)
			if n > 0 {
				mu.Lock()
				remaining := p.MaxBytes - buffer.Len()
				if remaining > 0 {
					if n > remaining {
						n = remaining
					}
					_, _ = buffer.Write(chunk[:n])
				}
				matched := p.Pattern.Match(buffer.Bytes())
				full := buffer.Len() >= p.MaxBytes
				mu.Unlock()
				if matched {
					result <- nil
					return
				}
				if full {
					result <- apperror.New(apperror.CodeResultTooLarge, "")
					return
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					result <- apperror.New(apperror.CodeCommandOutputUnparsable, "")
				} else {
					result <- apperror.Wrap(apperror.CodeCommandOutputUnparsable, "", readErr)
				}
				return
			}
		}
	}()
	select {
	case err := <-result:
		if err != nil {
			return "", err
		}
		return p.Family, nil
	case <-probeCtx.Done():
		_ = session.Close()
		return "", apperror.Wrap(apperror.CodeSSHTimeout, "", probeCtx.Err())
	}
}

// WrapHostKeyCallback marks host-key failures so Test can map them precisely.
func WrapHostKeyCallback(callback ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if callback == nil {
			return &knownHostError{cause: errors.New("host key verifier is not configured")}
		}
		if err := callback(hostname, remote, key); err != nil {
			return &knownHostError{cause: err}
		}
		return nil
	}
}

type knownHostError struct{ cause error }

func (e *knownHostError) Error() string { return "SSH host key verification failed" }
func (e *knownHostError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

var _ inventorysvc.ConnectionTester = (*Tester)(nil)
