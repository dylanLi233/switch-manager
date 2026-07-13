package sshprobe

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"golang.org/x/crypto/ssh"
)

type testServer struct {
	listener net.Listener
	signer   ssh.Signer
	done     chan struct{}
	once     sync.Once
}

func startServer(t *testing.T, password, prompt string) *testServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &testServer{listener: listener, signer: signer, done: make(chan struct{})}
	config := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, received []byte) (*ssh.Permissions, error) {
		if metadata.User() != "admin" || string(received) != password {
			return nil, errors.New("authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	go func() {
		defer close(server.done)
		for {
			raw, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveConnection(raw, config, prompt)
		}
	}()
	t.Cleanup(server.Close)
	return server
}

func serveConnection(raw net.Conn, config *ssh.ServerConfig, prompt string) {
	connection, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer connection.Close()
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, requests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range requests {
				switch request.Type {
				case "pty-req":
					_ = request.Reply(true, nil)
				case "shell":
					_ = request.Reply(true, nil)
					_, _ = channel.Write([]byte("welcome\r\n" + prompt))
					_, _ = channel.Read(make([]byte, 1))
					return
				default:
					_ = request.Reply(false, nil)
				}
			}
		}()
	}
}

func (s *testServer) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		_ = s.listener.Close()
		select {
		case <-s.done:
		case <-time.After(time.Second):
		}
	})
}

func (s *testServer) target() device.Device {
	host, portText, _ := net.SplitHostPort(s.listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return device.Device{Host: host, SSHPort: port}
}

func TestPasswordHandshakeAndPrompt(t *testing.T) {
	server := startServer(t, "secret", "FAKE> ")
	tester, err := New(ssh.FixedHostKey(server.signer.PublicKey()), time.Second, RegexPromptProber{Pattern: regexp.MustCompile(`FAKE> $`), Family: "fake", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tester.Test(context.Background(), server.target(), inventorysvc.AuthenticationMaterial{Type: credential.TypePassword, Username: "admin", Password: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TCPConnected || !result.SSHNegotiated || !result.Authenticated || !result.PromptDetected || result.PromptFamily != "fake" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHostKeyMismatchIsDistinct(t *testing.T) {
	server := startServer(t, "secret", "FAKE> ")
	_, otherPrivate, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherPrivate)
	tester, _ := New(ssh.FixedHostKey(otherSigner.PublicKey()), time.Second, nil)
	_, err := tester.Test(context.Background(), server.target(), inventorysvc.AuthenticationMaterial{Type: credential.TypePassword, Username: "admin", Password: []byte("secret")})
	if !apperror.IsCode(err, apperror.CodeHostKeyVerificationFailed) {
		t.Fatalf("error=%v", err)
	}
}

func TestWrongPasswordIsAuthenticationFailure(t *testing.T) {
	server := startServer(t, "secret", "FAKE> ")
	tester, _ := New(ssh.FixedHostKey(server.signer.PublicKey()), time.Second, nil)
	_, err := tester.Test(context.Background(), server.target(), inventorysvc.AuthenticationMaterial{Type: credential.TypePassword, Username: "admin", Password: []byte("wrong")})
	if !apperror.IsCode(err, apperror.CodeAuthenticationFailed) {
		t.Fatalf("error=%v", err)
	}
}
