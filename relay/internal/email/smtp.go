package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SMTP modes. STARTTLS on 587 is what transactional mail services offer by
// default; implicit TLS is port 465. ModePlain is only for a server on this
// machine (a local relay in development, or a test).
const (
	ModeSTARTTLS = "starttls"
	ModeTLS      = "tls"
	ModePlain    = "plain"
)

// SMTPConfig is one SMTP submission account.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the sender, an address or "Name <address>".
	From string
	Mode string
}

// SMTP sends through an SMTP submission server. It sends one message per
// connection: emails are few (a hub's results, verification codes).
type SMTP struct {
	cfg  SMTPConfig
	from *mail.Address
	now  func() time.Time
}

// NewSMTP checks the configuration and returns the provider.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	if cfg.Host == "" || cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, errors.New("the SMTP host and port are required")
	}
	from, err := mail.ParseAddress(strings.TrimSpace(cfg.From))
	if err != nil {
		return nil, errors.New("the sender must be an address or Name <address>")
	}
	switch cfg.Mode {
	case "":
		cfg.Mode = ModeSTARTTLS
	case ModeSTARTTLS, ModeTLS:
	case ModePlain:
		if !loopbackHost(cfg.Host) {
			return nil, errors.New("plain SMTP is allowed only to a server on this machine")
		}
	default:
		return nil, errors.New("the SMTP mode must be starttls, tls or plain")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("the SMTP user name and password go together")
	}
	return &SMTP{cfg: cfg, from: from, now: time.Now}, nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Send delivers one message. A permanent refusal (a 5xx answer to the sender,
// the recipient or the message) is ErrRejected; anything else that fails is
// ErrUnavailable, and may or may not have sent it.
func (s *SMTP) Send(ctx context.Context, msg Message) (string, error) {
	id, raw, err := s.compose(msg)
	if err != nil {
		return "", err
	}
	conn, err := s.dial(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return "", ErrUnavailable
	}
	defer client.Close()
	if s.cfg.Mode == ModeSTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", ErrUnavailable
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return "", ErrUnavailable
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return "", ErrUnavailable
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return "", smtpFailure(err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return "", smtpFailure(err)
	}
	w, err := client.Data()
	if err != nil {
		return "", smtpFailure(err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return "", ErrUnavailable
	}
	if err := w.Close(); err != nil {
		return "", smtpFailure(err)
	}
	_ = client.Quit()
	return id, nil
}

func (s *SMTP) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if s.cfg.Mode == ModeTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}}
		return tlsDialer.DialContext(ctx, "tcp", addr)
	}
	return dialer.DialContext(ctx, "tcp", addr)
}

// smtpFailure maps a server answer: 5xx is a refusal, the rest transient.
func smtpFailure(err error) error {
	var answer *textproto.Error
	if errors.As(err, &answer) && answer.Code >= 500 && answer.Code < 600 {
		return ErrRejected
	}
	return ErrUnavailable
}

// compose writes the message: plain text in UTF-8, quoted-printable, marked
// as sent by a machine so mail servers send no vacation replies to it.
func (s *SMTP) compose(msg Message) (string, []byte, error) {
	to, err := mail.ParseAddress(msg.To)
	if err != nil || to.Address != msg.To {
		return "", nil, ErrRejected
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return "", nil, ErrRejected
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	domain := s.from.Address[strings.LastIndex(s.from.Address, "@")+1:]
	id := hex.EncodeToString(nonce[:]) + "@" + domain
	var b bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	header("From", s.from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", s.now().UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+id+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	text := strings.ReplaceAll(strings.ReplaceAll(msg.Text, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(text)); err != nil {
		return "", nil, err
	}
	if err := qp.Close(); err != nil {
		return "", nil, err
	}
	return id, b.Bytes(), nil
}
