package email

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTPServer answers one SMTP session on loopback and records the
// envelope and the message. rejectRcpt answers 550 to RCPT.
type fakeSMTPServer struct {
	addr       string
	rejectRcpt bool
	got        chan smtpSession
}

type smtpSession struct {
	from, to string
	data     string
}

func startFakeSMTP(t *testing.T, rejectRcpt bool) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeSMTPServer{addr: ln.Addr().String(), rejectRcpt: rejectRcpt, got: make(chan smtpSession, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		r := bufio.NewReader(conn)
		write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
		write("220 fake ESMTP")
		var session smtpSession
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM:"):
				session.from = strings.TrimSpace(line[len("MAIL FROM:"):])
				write("250 ok")
			case strings.HasPrefix(cmd, "RCPT TO:"):
				if s.rejectRcpt {
					write("550 no such user")
					continue
				}
				session.to = strings.TrimSpace(line[len("RCPT TO:"):])
				write("250 ok")
			case cmd == "DATA":
				write("354 go ahead")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				session.data = b.String()
				write("250 queued")
				s.got <- session
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	return s
}

func newLoopbackSMTP(t *testing.T, addr string) *SMTP {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	var portNumber int
	for _, c := range port {
		portNumber = portNumber*10 + int(c-'0')
	}
	provider, err := NewSMTP(SMTPConfig{Host: host, Port: portNumber, From: "Isoform <notify@example.com>", Mode: ModePlain})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestSMTPSendsOnePlainTextMessage(t *testing.T) {
	server := startFakeSMTP(t, false)
	provider := newLoopbackSMTP(t, server.addr)
	id, err := provider.Send(context.Background(), Message{To: "owner@example.com", Subject: "Isoform：每日查看", Text: "Downloads 1,240\nD7 retention 31%"})
	if err != nil || !strings.HasSuffix(id, "@example.com") {
		t.Fatalf("send: %q %v", id, err)
	}
	session := <-server.got
	if session.from != "<notify@example.com>" || session.to != "<owner@example.com>" {
		t.Fatalf("envelope: %+v", session)
	}
	head, body, _ := strings.Cut(session.data, "\r\n\r\n")
	for _, want := range []string{"From: \"Isoform\" <notify@example.com>", "To: <owner@example.com>", "Subject: =?utf-8?q?", "Auto-Submitted: auto-generated", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header %q missing in:\n%s", want, head)
		}
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "Downloads 1,240\r\nD7 retention 31%") {
		t.Fatalf("body: %q", decoded)
	}
}

func TestSMTPRefusalIsRejected(t *testing.T) {
	server := startFakeSMTP(t, true)
	provider := newLoopbackSMTP(t, server.addr)
	if _, err := provider.Send(context.Background(), Message{To: "owner@example.com", Subject: "Isoform: x", Text: "y"}); !errors.Is(err, ErrRejected) {
		t.Fatalf("550 must be a refusal: %v", err)
	}
	if _, err := provider.Send(context.Background(), Message{To: "owner@example.com", Subject: "Isoform: x\r\nBcc: someone@example.com", Text: "y"}); !errors.Is(err, ErrRejected) {
		t.Fatalf("a header break must be refused: %v", err)
	}
}

func TestNewSMTPRequiresTLSForRemoteHosts(t *testing.T) {
	if _, err := NewSMTP(SMTPConfig{Host: "smtp.example.com", Port: 25, From: "notify@example.com", Mode: ModePlain}); err == nil {
		t.Fatal("plain SMTP to a remote host must be refused")
	}
	if _, err := NewSMTP(SMTPConfig{Host: "smtp.example.com", Port: 587, From: "not an address"}); err == nil {
		t.Fatal("a bad sender must be refused")
	}
}
