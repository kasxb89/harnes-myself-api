package common

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSMTPServer struct {
	listener          net.Listener
	host              string
	port              int
	cert              tls.Certificate
	advertiseSTARTTLS bool
	authMechanisms    []string
	messages          chan string
	authCommands      chan string
	startTLSCommands  chan string
}

func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	return newFakeSMTPServerWithSTARTTLSAdvertisement(t, true)
}

func newFakeSMTPServerWithSTARTTLSAdvertisement(t *testing.T, advertiseSTARTTLS bool) *fakeSMTPServer {
	t.Helper()

	cert, err := newTestTLSCertificate()
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	server := &fakeSMTPServer{
		listener:          listener,
		host:              host,
		port:              port,
		cert:              cert,
		advertiseSTARTTLS: advertiseSTARTTLS,
		authMechanisms:    []string{"PLAIN", "LOGIN"},
		messages:          make(chan string, 1),
		authCommands:      make(chan string, 1),
		startTLSCommands:  make(chan string, 1),
	}
	go server.serve()
	return server
}

func newFakeImplicitTLSSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()

	cert, err := newTestTLSCertificate()
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	server := &fakeSMTPServer{
		listener:          tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}}),
		host:              host,
		port:              port,
		cert:              cert,
		advertiseSTARTTLS: false,
		authMechanisms:    []string{"PLAIN", "LOGIN"},
		messages:          make(chan string, 1),
		authCommands:      make(chan string, 1),
		startTLSCommands:  make(chan string, 1),
	}
	go server.serve()
	return server
}

func (s *fakeSMTPServer) close() {
	_ = s.listener.Close()
}

func (s *fakeSMTPServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if err := writeSMTPLine(rw, "220 fake.smtp.local ESMTP"); err != nil {
		return
	}

	encrypted := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		upperCommand := strings.ToUpper(command)

		switch {
		case strings.HasPrefix(upperCommand, "EHLO"):
			if err := writeSMTPLine(rw, "250-fake.smtp.local"); err != nil {
				return
			}
			if !encrypted && s.advertiseSTARTTLS {
				if err := writeSMTPLine(rw, "250-STARTTLS"); err != nil {
					return
				}
			}
			if len(s.authMechanisms) > 0 {
				if err := writeSMTPLine(rw, "250 AUTH "+strings.Join(s.authMechanisms, " ")); err != nil {
					return
				}
			} else if err := writeSMTPLine(rw, "250 8BITMIME"); err != nil {
				return
			}
		case upperCommand == "STARTTLS":
			if encrypted || !s.advertiseSTARTTLS {
				if err := writeSMTPLine(rw, "502 5.5.1 STARTTLS not supported"); err != nil {
					return
				}
				continue
			}
			select {
			case s.startTLSCommands <- command:
			default:
			}
			if err := writeSMTPLine(rw, "220 2.0.0 Ready to start TLS"); err != nil {
				return
			}
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
			encrypted = true
		case strings.HasPrefix(upperCommand, "AUTH"):
			select {
			case s.authCommands <- command:
			default:
			}
			if err := writeSMTPLine(rw, "235 2.7.0 Authentication successful"); err != nil {
				return
			}
		case strings.HasPrefix(upperCommand, "MAIL FROM:"):
			if err := writeSMTPLine(rw, "250 2.1.0 Sender OK"); err != nil {
				return
			}
		case strings.HasPrefix(upperCommand, "RCPT TO:"):
			if err := writeSMTPLine(rw, "250 2.1.5 Recipient OK"); err != nil {
				return
			}
		case upperCommand == "DATA":
			if err := writeSMTPLine(rw, "354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return
			}
			var data strings.Builder
			for {
				dataLine, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				data.WriteString(dataLine)
			}
			s.messages <- data.String()
			if err := writeSMTPLine(rw, "250 2.0.0 Queued"); err != nil {
				return
			}
		case upperCommand == "QUIT":
			_ = writeSMTPLine(rw, "221 2.0.0 Bye")
			return
		default:
			if err := writeSMTPLine(rw, "502 5.5.1 Command not implemented"); err != nil {
				return
			}
		}
	}
}

func writeSMTPLine(rw *bufio.ReadWriter, line string) error {
	_, err := rw.WriteString(line + "\r\n")
	if err != nil {
		return err
	}
	return rw.Flush()
}

func newTestTLSCertificate() (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "aixinexchange01.aixin-chip.com",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"aixinexchange01", "aixinexchange01.aixin-chip.com"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func withSMTPSettings(t *testing.T) {
	t.Helper()
	originalSMTPServer := SMTPServer
	originalSMTPPort := SMTPPort
	originalSMTPSSLEnabled := SMTPSSLEnabled
	originalSMTPStartTLSEnabled := SMTPStartTLSEnabled
	originalSMTPInsecureSkipVerify := SMTPInsecureSkipVerify
	originalSMTPForceAuthLogin := SMTPForceAuthLogin
	originalSMTPAccount := SMTPAccount
	originalSMTPFrom := SMTPFrom
	originalSMTPToken := SMTPToken
	originalSystemName := SystemName

	t.Cleanup(func() {
		SMTPServer = originalSMTPServer
		SMTPPort = originalSMTPPort
		SMTPSSLEnabled = originalSMTPSSLEnabled
		SMTPStartTLSEnabled = originalSMTPStartTLSEnabled
		SMTPInsecureSkipVerify = originalSMTPInsecureSkipVerify
		SMTPForceAuthLogin = originalSMTPForceAuthLogin
		SMTPAccount = originalSMTPAccount
		SMTPFrom = originalSMTPFrom
		SMTPToken = originalSMTPToken
		SystemName = originalSystemName
	})
}

func TestSendEmailUsesExplicitStartTLSWithInsecureCertificate(t *testing.T) {
	server := newFakeSMTPServer(t)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = true
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case message := <-server.messages:
		require.Contains(t, message, "Subject: =?UTF-8?B?")
		require.Contains(t, message, "<p>123456</p>")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP DATA")
	}
}

func TestSendEmailExplicitStartTLSRequiresServerSupport(t *testing.T) {
	server := newFakeSMTPServerWithSTARTTLSAdvertisement(t, false)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = true
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.Error(t, err)
	require.Contains(t, err.Error(), "STARTTLS")
}

func TestSendEmailDoesNotAutoUpgradeWhenStartTLSDisabled(t *testing.T) {
	server := newFakeSMTPServerWithSTARTTLSAdvertisement(t, true)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = false
	SMTPInsecureSkipVerify = false
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case command := <-server.startTLSCommands:
		t.Fatalf("unexpected SMTP STARTTLS command: %s", command)
	default:
	}

	select {
	case message := <-server.messages:
		require.Contains(t, message, "<p>123456</p>")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP DATA")
	}
}

func TestSMTPPlainAuthRejectsRemotePlaintextConnection(t *testing.T) {
	server := newFakeSMTPServerWithSTARTTLSAdvertisement(t, false)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = "smtp.example.com"
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = false
	SMTPInsecureSkipVerify = false
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = "secret"

	conn, err := net.Dial("tcp", net.JoinHostPort(server.host, strconv.Itoa(server.port)))
	require.NoError(t, err)
	client, err := smtp.NewClient(conn, SMTPServer)
	require.NoError(t, err)

	err = client.Auth(getSMTPAuth())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unencrypted connection")

	select {
	case command := <-server.authCommands:
		t.Fatalf("unexpected SMTP auth command: %s", command)
	default:
	}
}

func TestNewSMTPClientHonorsExplicitStartTLSWhenPortIs465(t *testing.T) {
	server := newFakeSMTPServer(t)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = 465
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = true

	client, err := newSMTPClient(fmt.Sprintf("%s:%d", server.host, server.port))
	require.NoError(t, err)
	defer client.Close()

	select {
	case command := <-server.startTLSCommands:
		require.Equal(t, "STARTTLS", command)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP STARTTLS")
	}
}

func TestNewSMTPClientKeepsImplicitTLSForLegacyPort465(t *testing.T) {
	server := newFakeImplicitTLSSMTPServer(t)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = 465
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = false
	SMTPInsecureSkipVerify = true

	client, err := newSMTPClient(fmt.Sprintf("%s:%d", server.host, server.port))
	require.NoError(t, err)
	defer client.Close()
}

func TestSendEmailSkipsAuthWhenCredentialsAreEmpty(t *testing.T) {
	server := newFakeSMTPServerWithSTARTTLSAdvertisement(t, false)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = false
	SMTPInsecureSkipVerify = false
	SMTPForceAuthLogin = false
	SMTPAccount = ""
	SMTPFrom = "sender@example.com"
	SMTPToken = ""
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case command := <-server.authCommands:
		t.Fatalf("unexpected SMTP auth command: %s", command)
	default:
	}

	select {
	case message := <-server.messages:
		require.Contains(t, message, "<p>123456</p>")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP DATA")
	}
}

func TestSendEmailSkipsAuthWhenCredentialsAreIncomplete(t *testing.T) {
	server := newFakeSMTPServerWithSTARTTLSAdvertisement(t, false)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = false
	SMTPInsecureSkipVerify = false
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = ""
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case command := <-server.authCommands:
		t.Fatalf("unexpected SMTP auth command: %s", command)
	default:
	}

	select {
	case message := <-server.messages:
		require.Contains(t, message, "<p>123456</p>")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP DATA")
	}
}

func TestSendEmailUsesNTLMWhenServerOnlySupportsNTLM(t *testing.T) {
	server := newFakeSMTPServer(t)
	server.authMechanisms = []string{"NTLM"}
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = true
	SMTPForceAuthLogin = false
	SMTPAccount = "no-reply"
	SMTPFrom = "no-reply@example.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case command := <-server.authCommands:
		require.True(t, strings.HasPrefix(command, "AUTH NTLM "), "unexpected auth command: %s", command)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP AUTH")
	}
}

func TestSendEmailUsesNTLMForMicrosoftAccountWhenServerOnlySupportsNTLM(t *testing.T) {
	server := newFakeSMTPServer(t)
	server.authMechanisms = []string{"NTLM"}
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = true
	SMTPForceAuthLogin = false
	SMTPAccount = "no-reply@contoso.onmicrosoft.com"
	SMTPFrom = "no-reply@contoso.onmicrosoft.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.NoError(t, err)

	select {
	case command := <-server.authCommands:
		require.True(t, strings.HasPrefix(command, "AUTH NTLM "), "unexpected auth command: %s", command)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP AUTH")
	}
}

func TestSendEmailExplicitStartTLSRejectsUntrustedCertificateByDefault(t *testing.T) {
	server := newFakeSMTPServer(t)
	defer server.close()
	withSMTPSettings(t)

	SMTPServer = server.host
	SMTPPort = server.port
	SMTPSSLEnabled = false
	SMTPStartTLSEnabled = true
	SMTPInsecureSkipVerify = false
	SMTPForceAuthLogin = false
	SMTPAccount = "sender@example.com"
	SMTPFrom = "sender@example.com"
	SMTPToken = "secret"
	SystemName = "New API"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "certificate")
}

type demandSMTPScenario struct {
	finalCode       int
	dropFinal       bool
	dropQuit        bool
	rejectRecipient bool
	stallGreeting   bool
	stallFinal      bool
	username        string
	password        string
}
type demandSMTPTestServer struct {
	listener net.Listener
	address  string
	port     int
	commands chan string
	messages chan string
	done     chan struct{}
	mu       sync.Mutex
	conn     net.Conn
}

func newDemandSMTPTestServer(t *testing.T, scenario demandSMTPScenario) *demandSMTPTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	server := &demandSMTPTestServer{listener: listener, address: host, port: n, commands: make(chan string, 32), messages: make(chan string, 1), done: make(chan struct{})}
	go func() {
		defer close(server.done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		server.conn = conn
		server.mu.Unlock()
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		if scenario.stallGreeting {
			var b [1]byte
			_, _ = conn.Read(b[:])
			return
		}
		if writeSMTPLine(rw, "220 local-demand-test ESMTP") != nil {
			return
		}
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			server.commands <- line
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				if writeSMTPLine(rw, "250-local-demand-test") != nil {
					return
				}
				if writeSMTPLine(rw, "250 AUTH PLAIN LOGIN NTLM") != nil {
					return
				}
			case strings.HasPrefix(upper, "AUTH LOGIN"):
				if writeSMTPLine(rw, "334 "+base64.StdEncoding.EncodeToString([]byte("Username:"))) != nil {
					return
				}
				user, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				u, e := base64.StdEncoding.DecodeString(strings.TrimSpace(user))
				if e != nil {
					return
				}
				if writeSMTPLine(rw, "334 "+base64.StdEncoding.EncodeToString([]byte("Password:"))) != nil {
					return
				}
				pass, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				p, e := base64.StdEncoding.DecodeString(strings.TrimSpace(pass))
				if e != nil {
					return
				}
				if string(u) != scenario.username || string(p) != scenario.password {
					_ = writeSMTPLine(rw, "535 authentication rejected")
					return
				}
				if writeSMTPLine(rw, "235 authenticated") != nil {
					return
				}
			case strings.HasPrefix(upper, "AUTH"):
				if writeSMTPLine(rw, "235 authenticated") != nil {
					return
				}
			case strings.HasPrefix(upper, "MAIL FROM:"):
				if writeSMTPLine(rw, "250 sender accepted") != nil {
					return
				}
			case strings.HasPrefix(upper, "RCPT TO:"):
				if scenario.rejectRecipient {
					_ = writeSMTPLine(rw, "550 secret-bearing response must never escape")
					return
				}
				if writeSMTPLine(rw, "250 recipient accepted") != nil {
					return
				}
			case upper == "DATA":
				if writeSMTPLine(rw, "354 send body") != nil {
					return
				}
				var content strings.Builder
				for {
					line, err := rw.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimRight(line, "\r\n") == "." {
						break
					}
					content.WriteString(line)
				}
				server.messages <- content.String()
				if scenario.dropFinal {
					return
				}
				if scenario.stallFinal {
					var b [1]byte
					_, _ = conn.Read(b[:])
					return
				}
				code := scenario.finalCode
				if code == 0 {
					code = 250
				}
				if writeSMTPLine(rw, fmt.Sprintf("%d local result", code)) != nil {
					return
				}
			case upper == "QUIT":
				if !scenario.dropQuit {
					_ = writeSMTPLine(rw, "221 bye")
				}
				return
			default:
				if writeSMTPLine(rw, "502 unsupported") != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		server.mu.Lock()
		if server.conn != nil {
			_ = server.conn.Close()
		}
		server.mu.Unlock()
		select {
		case <-server.done:
		case <-time.After(2 * time.Second):
			t.Error("local SMTP handler did not terminate")
		}
	})
	return server
}
func demandSMTPTestConfig(server *demandSMTPTestServer) DemandSMTPConfig {
	return DemandSMTPConfig{Server: server.address, Port: server.port, From: "no-reply@example.invalid", Name: "Local demand test", Timeout: time.Second}
}
func demandSMTPTestMessage(recipient string) DemandSMTPMessage {
	return DemandSMTPMessage{Recipient: recipient, Subject: "需求消息", Text: "文本摘要 <script>is literal text</script>", MessageID: "<stable-demand-notification@example.invalid>"}
}
func TestDemandSMTPAcceptsEachRecipientIndependentlyAndIgnoresQuitFailure(t *testing.T) {
	for _, recipient := range []string{"first@example.invalid", "second@example.invalid"} {
		t.Run(recipient, func(t *testing.T) {
			server := newDemandSMTPTestServer(t, demandSMTPScenario{dropQuit: true})
			fences := 0
			result := SendDemandEmail(context.Background(), demandSMTPTestConfig(server), demandSMTPTestMessage(recipient), func() error { fences++; return nil })
			require.Equal(t, "accepted", result.State)
			assert.Equal(t, "smtp_accepted_quit_failed", result.Code)
			assert.False(t, result.Retryable)
			assert.Equal(t, 1, fences)
			raw := <-server.messages
			message, err := mail.ReadMessage(strings.NewReader(raw))
			require.NoError(t, err)
			to, err := message.Header.AddressList("To")
			require.NoError(t, err)
			require.Len(t, to, 1)
			assert.Equal(t, recipient, to[0].Address)
			assert.Empty(t, message.Header.Get("Cc"))
			assert.Contains(t, message.Header.Get("Content-Type"), "text/plain")
			decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
			require.NoError(t, err)
			assert.Equal(t, demandSMTPTestMessage(recipient).Text, string(decoded))
			assert.Equal(t, demandSMTPTestMessage(recipient).MessageID, message.Header.Get("Message-ID"))
			subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
			require.NoError(t, err)
			assert.Equal(t, "需求消息", subject)
			<-server.done
			rcpts := []string{}
			for len(server.commands) > 0 {
				cmd := <-server.commands
				if strings.HasPrefix(cmd, "RCPT TO:") {
					rcpts = append(rcpts, cmd)
				}
			}
			assert.Equal(t, []string{"RCPT TO:<" + recipient + ">"}, rcpts)
		})
	}
}
func TestDemandSMTPDistinguishesFinalRejectionAndUnknownConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario demandSMTPScenario
		state    string
		retry    bool
	}{{"lost-final", demandSMTPScenario{dropFinal: true}, "unknown", false}, {"temporary-reject", demandSMTPScenario{finalCode: 450}, "failed", true}, {"permanent-reject", demandSMTPScenario{finalCode: 550}, "failed", false}, {"recipient-reject", demandSMTPScenario{rejectRecipient: true}, "failed", false}} {
		t.Run(tc.name, func(t *testing.T) {
			server := newDemandSMTPTestServer(t, tc.scenario)
			result := SendDemandEmail(context.Background(), demandSMTPTestConfig(server), demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
			assert.Equal(t, tc.state, result.State)
			assert.Equal(t, tc.retry, result.Retryable)
			assert.NotContains(t, fmt.Sprint(result), "secret-bearing")
		})
	}
}
func TestDemandSMTPFenceFailureAndHeaderInjectionSendNoMessage(t *testing.T) {
	server := newDemandSMTPTestServer(t, demandSMTPScenario{})
	result := SendDemandEmail(context.Background(), demandSMTPTestConfig(server), demandSMTPTestMessage("first@example.invalid"), func() error { return errors.New("private database failure") })
	assert.Equal(t, "not_sent", result.State)
	assert.Equal(t, "smtp_fence_rejected", result.Code)
	assert.NotContains(t, fmt.Sprint(result), "private database")
	<-server.done
	assert.Empty(t, server.messages)
	for _, tc := range []struct {
		name   string
		change func(*DemandSMTPMessage)
	}{{"recipient", func(m *DemandSMTPMessage) { m.Recipient = "first@example.invalid\r\nCc: injected@example.invalid" }}, {"multiple recipients", func(m *DemandSMTPMessage) { m.Recipient = "first@example.invalid, second@example.invalid" }}, {"subject", func(m *DemandSMTPMessage) { m.Subject = "hello\r\nBcc: injected@example.invalid" }}, {"message-id", func(m *DemandSMTPMessage) { m.MessageID = "<one@example.invalid>\r\nCc: injected@example.invalid" }}} {
		t.Run(tc.name, func(t *testing.T) {
			message := demandSMTPTestMessage("first@example.invalid")
			tc.change(&message)
			called := false
			result := SendDemandEmail(context.Background(), DemandSMTPConfig{Server: "127.0.0.1", Port: 1, From: "no-reply@example.invalid"}, message, func() error { called = true; return nil })
			assert.Equal(t, "smtp_invalid_message", result.Code)
			assert.False(t, called)
		})
	}
}
func TestDemandSMTPTimeoutAndCancellationCloseOwnedConnections(t *testing.T) {
	t.Run("bounded greeting", func(t *testing.T) {
		server := newDemandSMTPTestServer(t, demandSMTPScenario{stallGreeting: true})
		cfg := demandSMTPTestConfig(server)
		cfg.Timeout = 50 * time.Millisecond
		result := SendDemandEmail(context.Background(), cfg, demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
		assert.Equal(t, "failed", result.State)
		assert.Equal(t, "smtp_timeout", result.Code)
		<-server.done
	})
	t.Run("cancel after body", func(t *testing.T) {
		server := newDemandSMTPTestServer(t, demandSMTPScenario{stallFinal: true})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan DemandSMTPResult, 1)
		go func() {
			done <- SendDemandEmail(ctx, demandSMTPTestConfig(server), demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
		}()
		<-server.messages
		cancel()
		select {
		case result := <-done:
			assert.Equal(t, "unknown", result.State)
			assert.False(t, result.Retryable)
		case <-time.After(2 * time.Second):
			t.Fatal("canceled SMTP call did not return")
		}
		<-server.done
	})
}
func TestDemandSMTPSnapshotAndAuthenticationNeverRereadGlobals(t *testing.T) {
	server := newDemandSMTPTestServer(t, demandSMTPScenario{username: "snapshot@example.invalid", password: "snapshot-secret"})
	withSMTPSettings(t)
	OptionMapRWMutex.Lock()
	original := OptionMap
	OptionMap = map[string]string{"SMTPServer": server.address, "SMTPPort": strconv.Itoa(server.port), "SMTPFrom": "snapshot@example.invalid", "SMTPAccount": "snapshot@example.invalid", "SMTPToken": "snapshot-secret", "SMTPForceAuthLogin": "true", "SystemName": "Snapshot sender"}
	OptionMapRWMutex.Unlock()
	t.Cleanup(func() { OptionMapRWMutex.Lock(); OptionMap = original; OptionMapRWMutex.Unlock() })
	cfg := SnapshotDemandSMTPConfig()
	encoded, err := Marshal(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "snapshot-secret")
	cfg.Timeout = time.Second
	OptionMapRWMutex.Lock()
	SMTPServer = "must-never-be-dialed.invalid"
	SMTPAccount = "changed@example.invalid"
	SMTPFrom = "changed@example.invalid"
	SMTPToken = "changed-secret"
	SMTPForceAuthLogin = false
	OptionMap = map[string]string{"SMTPServer": "must-never-be-dialed.invalid"}
	OptionMapRWMutex.Unlock()
	result := SendDemandEmail(context.Background(), cfg, demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
	assert.Equal(t, "accepted", result.State)
	raw := <-server.messages
	assert.Contains(t, raw, "snapshot@example.invalid")
	assert.NotContains(t, raw, "changed@example.invalid")
	assert.NotContains(t, raw, "snapshot-secret")
	assert.NotContains(t, raw, "changed-secret")
	<-server.done
	auth := []string{}
	for len(server.commands) > 0 {
		cmd := <-server.commands
		if strings.HasPrefix(cmd, "AUTH ") {
			auth = append(auth, cmd)
		}
	}
	assert.Equal(t, []string{"AUTH LOGIN"}, auth)
	// Compatibility selection is immutable too: Outlook preference still permits
	// an NTLM-only server, while explicit ForceAuthLogin keeps LOGIN.
	for _, tc := range []struct {
		force bool
		want  string
	}{{false, "NTLM"}, {true, "LOGIN"}} {
		a := &demandSMTPAuth{server: "snapshot.example.invalid", username: "user@outlook.invalid", password: "secret", preferLogin: true, forceLogin: tc.force}
		mechanism, _, err := a.Start(&smtp.ServerInfo{Name: "snapshot.example.invalid", Auth: []string{"NTLM"}})
		require.NoError(t, err)
		assert.Equal(t, tc.want, mechanism)
	}
}
func TestDemandSMTPTLSOptionsUseOwnedBoundedTransport(t *testing.T) {
	t.Run("explicit STARTTLS", func(t *testing.T) {
		server := newFakeSMTPServer(t)
		defer server.close()
		cfg := DemandSMTPConfig{Server: server.host, Port: server.port, From: "sender@example.invalid", StartTLS: true, InsecureSkipVerify: true, Timeout: 2 * time.Second}
		result := SendDemandEmail(context.Background(), cfg, demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
		assert.Equal(t, "accepted", result.State)
		assert.Equal(t, "STARTTLS", <-server.startTLSCommands)
	})
	t.Run("implicit TLS", func(t *testing.T) {
		server := newFakeImplicitTLSSMTPServer(t)
		defer server.close()
		cfg := DemandSMTPConfig{Server: server.host, Port: server.port, From: "sender@example.invalid", SSL: true, InsecureSkipVerify: true, Timeout: 2 * time.Second}
		result := SendDemandEmail(context.Background(), cfg, demandSMTPTestMessage("first@example.invalid"), func() error { return nil })
		assert.Equal(t, "accepted", result.State)
	})
	t.Run("untrusted TLS rejected", func(t *testing.T) {
		server := newFakeImplicitTLSSMTPServer(t)
		defer server.close()
		cfg := DemandSMTPConfig{Server: server.host, Port: server.port, From: "sender@example.invalid", SSL: true, Timeout: 2 * time.Second}
		called := false
		result := SendDemandEmail(context.Background(), cfg, demandSMTPTestMessage("first@example.invalid"), func() error { called = true; return nil })
		assert.Equal(t, "failed", result.State)
		assert.Equal(t, "tls", result.Stage)
		assert.False(t, called)
	})
}
