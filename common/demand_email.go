package common

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	ntlmssp "github.com/Azure/go-ntlmssp"
)

// DemandSMTPConfig is captured per attempt; sending and authentication never
// reread mutable SMTP globals. Timeout is always capped at twenty seconds.
type DemandSMTPConfig struct {
	Server                                            string
	Port                                              int
	Account, From, Name                               string
	Token                                             string `json:"-"`
	SSL, StartTLS, InsecureSkipVerify, ForceAuthLogin bool
	PreferLogin                                       bool
	Timeout                                           time.Duration
}

func SnapshotDemandSMTPConfig() DemandSMTPConfig {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	var cfg DemandSMTPConfig
	if _, ok := OptionMap["SMTPServer"]; ok {
		// The option map is updated under this same lock. Prefer it to SMTPFrom,
		// whose legacy verification-mail fallback can still write its old global.
		cfg.Server, cfg.Account, cfg.Token = OptionMap["SMTPServer"], OptionMap["SMTPAccount"], OptionMap["SMTPToken"]
		cfg.From, cfg.Name = OptionMap["SMTPFrom"], OptionMap["SystemName"]
		cfg.Port, _ = strconv.Atoi(OptionMap["SMTPPort"])
		cfg.SSL, _ = strconv.ParseBool(OptionMap["SMTPSSLEnabled"])
		cfg.StartTLS, _ = strconv.ParseBool(OptionMap["SMTPStartTLSEnabled"])
		cfg.InsecureSkipVerify, _ = strconv.ParseBool(OptionMap["SMTPInsecureSkipVerify"])
		cfg.ForceAuthLogin, _ = strconv.ParseBool(OptionMap["SMTPForceAuthLogin"])
	} else {
		// Startup/tests may capture before InitOptionMap. Workers start after options
		// initialization, where the map branch above is authoritative.
		cfg = DemandSMTPConfig{Server: SMTPServer, Port: SMTPPort, Account: SMTPAccount, Token: SMTPToken, From: SMTPFrom, Name: SystemName, SSL: SMTPSSLEnabled, StartTLS: SMTPStartTLSEnabled, InsecureSkipVerify: SMTPInsecureSkipVerify, ForceAuthLogin: SMTPForceAuthLogin}
	}
	if cfg.From == "" {
		cfg.From = cfg.Account
	}
	cfg.PreferLogin = isOutlookServer(cfg.Account) || slices.Contains(EmailLoginAuthServerList, cfg.Server)
	return cfg
}

type DemandSMTPMessage struct{ Recipient, Subject, Text, MessageID string }

// accepted means the SMTP server confirmed DATA, not that an inbox received it.
// unknown must not be automatically retried, including after process recovery.
type DemandSMTPResult struct {
	State     string
	Code      string
	Stage     string
	Retryable bool
}

// SendDemandEmail owns and closes its connection. beforeData is a final durable
// fence/config check after DATA354 and before payload bytes; it must honor the
// caller's context for any database work. No background send outlives this call.
func SendDemandEmail(ctx context.Context, cfg DemandSMTPConfig, msg DemandSMTPMessage, beforeData func() error) DemandSMTPResult {
	if cfg.From == "" {
		cfg.From = cfg.Account
	}
	failed := func(code, stage string, retry bool) DemandSMTPResult {
		return DemandSMTPResult{"failed", code, stage, retry}
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil || !demandMailHeaderSafe(cfg.From) || cfg.Server == "" || strings.ContainsAny(cfg.Server, "\r\n\x00") || cfg.Port < 1 || cfg.Port > 65535 {
		return failed("smtp_not_configured", "validation", false)
	}
	recipient, err := mail.ParseAddress(msg.Recipient)
	if err != nil || recipient.Name != "" || recipient.Address != strings.TrimSpace(msg.Recipient) || !demandMailHeaderSafe(msg.Recipient) || !demandMailHeaderSafe(msg.Subject) || !demandMailHeaderSafe(cfg.Name) || !demandMessageIDValid(msg.MessageID) || beforeData == nil {
		return failed("smtp_invalid_message", "validation", false)
	}
	timeout := cfg.Timeout
	if timeout <= 0 || timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(cfg.Server, strconv.Itoa(cfg.Port)))
	if err != nil {
		return demandSMTPPreDataFailure(ctx, "connect", err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(deadline); err != nil {
		return failed("smtp_connect_failed", "connect", true)
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(canceled) })
	defer func() {
		if !stop() {
			<-canceled
		}
	}()
	tlsConfig := &tls.Config{ServerName: cfg.Server, InsecureSkipVerify: cfg.InsecureSkipVerify} // #nosec G402 -- preserves the existing administrator-controlled SMTP option.
	var stream net.Conn = conn
	if cfg.SSL || cfg.Port == 465 && !cfg.StartTLS {
		encrypted := tls.Client(conn, tlsConfig)
		if err = encrypted.HandshakeContext(ctx); err != nil {
			return demandSMTPPreDataFailure(ctx, "tls", err)
		}
		stream = encrypted
	}
	client, err := smtp.NewClient(stream, cfg.Server)
	if err != nil {
		return demandSMTPPreDataFailure(ctx, "greeting", err)
	}
	defer client.Close()
	if cfg.StartTLS && !cfg.SSL {
		supported, _ := client.Extension("STARTTLS")
		if !supported {
			return failed("smtp_tls_unavailable", "tls", false)
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return demandSMTPPreDataFailure(ctx, "tls", err)
		}
	}
	if cfg.Account != "" && cfg.Token != "" {
		auth := &demandSMTPAuth{server: cfg.Server, username: cfg.Account, password: cfg.Token, forceLogin: cfg.ForceAuthLogin, preferLogin: cfg.PreferLogin || isOutlookServer(cfg.Account)}
		if err = client.Auth(auth); err != nil {
			return demandSMTPPreDataFailure(ctx, "auth", err)
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return demandSMTPPreDataFailure(ctx, "sender", err)
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return demandSMTPPreDataFailure(ctx, "recipient", err)
	}
	writer, err := client.Data()
	if err != nil {
		return demandSMTPPreDataFailure(ctx, "data", err)
	}
	if err = beforeData(); err != nil {
		return DemandSMTPResult{"not_sent", "smtp_fence_rejected", "fence", false}
	}
	if ctx.Err() != nil {
		return DemandSMTPResult{"not_sent", "smtp_canceled", "fence", false}
	}
	// Header fields are validated, and body text is MIME encoded instead of being
	// interpolated into headers or HTML. Each attempt has exactly one envelope/To.
	encoded := base64.StdEncoding.EncodeToString([]byte(msg.Text))
	var body strings.Builder
	for len(encoded) > 76 {
		body.WriteString(encoded[:76])
		body.WriteString("\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded)
	body.WriteString("\r\n")
	displayFrom := (&mail.Address{Name: cfg.Name, Address: from.Address}).String()
	payload := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s", displayFrom, recipient.String(), mime.BEncoding.Encode("UTF-8", msg.Subject), time.Now().Format(time.RFC1123Z), msg.MessageID, body.String())
	if _, err = writer.Write([]byte(payload)); err != nil {
		return DemandSMTPResult{"unknown", "smtp_result_unknown", "payload", false}
	}
	if err = writer.Close(); err != nil {
		var rejection *textproto.Error
		if errors.As(err, &rejection) && rejection.Code >= 400 && rejection.Code <= 599 {
			return failed("smtp_data_rejected", "confirmation", rejection.Code < 500)
		}
		return DemandSMTPResult{"unknown", "smtp_result_unknown", "confirmation", false}
	}
	// DATA250 is the acceptance boundary. A lost/rejected QUIT cannot turn this
	// into a retryable failure. Keep polite teardown short after acceptance.
	quitDeadline := time.Now().Add(time.Second)
	if deadline.Before(quitDeadline) {
		quitDeadline = deadline
	}
	_ = conn.SetDeadline(quitDeadline)
	if err = client.Quit(); err != nil {
		return DemandSMTPResult{"accepted", "smtp_accepted_quit_failed", "quit", false}
	}
	return DemandSMTPResult{"accepted", "smtp_accepted", "confirmation", false}
}

func demandMailHeaderSafe(value string) bool {
	return !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) })
}
func demandMessageIDValid(value string) bool {
	if len(value) < 5 || len(value) > 255 || value[0] != '<' || value[len(value)-1] != '>' || !demandMailHeaderSafe(value) {
		return false
	}
	inner := value[1 : len(value)-1]
	return strings.Count(inner, "@") == 1 && !strings.ContainsAny(inner, " <>\t")
}
func demandSMTPPreDataFailure(ctx context.Context, stage string, err error) DemandSMTPResult {
	if errors.Is(ctx.Err(), context.Canceled) {
		return DemandSMTPResult{"not_sent", "smtp_canceled", stage, false}
	}
	var network net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return DemandSMTPResult{"failed", "smtp_timeout", stage, true}
	}
	var rejection *textproto.Error
	if errors.As(err, &rejection) {
		return DemandSMTPResult{"failed", "smtp_" + stage + "_rejected", stage, rejection.Code >= 400 && rejection.Code < 500}
	}
	retry := stage == "connect" || stage == "greeting" || stage == "sender" || stage == "recipient" || stage == "data"
	return DemandSMTPResult{"failed", "smtp_" + stage + "_failed", stage, retry}
}

type demandSMTPAuth struct {
	server, username, password, mechanism string
	forceLogin, preferLogin               bool
}

func (a *demandSMTPAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	login := a.forceLogin || a.preferLogin && !(server != nil && len(server.Auth) == 1 && smtpServerSupportsAuth(server, "NTLM"))
	if login {
		a.mechanism = "LOGIN"
		return "LOGIN", []byte{}, nil
	}
	switch {
	case smtpServerSupportsAuth(server, "PLAIN"):
		a.mechanism = "PLAIN"
		return smtp.PlainAuth("", a.username, a.password, a.server).Start(server)
	case smtpServerSupportsAuth(server, "LOGIN"):
		a.mechanism = "LOGIN"
		return "LOGIN", []byte{}, nil
	case smtpServerSupportsAuth(server, "NTLM"):
		a.mechanism = "NTLM"
		b, err := ntlmssp.NewNegotiateMessage("", "")
		return "NTLM", b, err
	default:
		a.mechanism = "PLAIN"
		return smtp.PlainAuth("", a.username, a.password, a.server).Start(server)
	}
}
func (a *demandSMTPAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch a.mechanism {
	case "LOGIN":
		switch string(challenge) {
		case "Username:":
			return []byte(a.username), nil
		case "Password:":
			return []byte(a.password), nil
		default:
			return nil, errors.New("unexpected SMTP LOGIN challenge")
		}
	case "NTLM":
		return ntlmssp.NewAuthenticateMessage(challenge, a.username, a.password, nil)
	default:
		return nil, errors.New("unexpected SMTP authentication challenge")
	}
}
