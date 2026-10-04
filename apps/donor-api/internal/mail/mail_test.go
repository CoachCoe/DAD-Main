package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMessageMIMEAndInjectionBoundaries(t *testing.T) {
	message := Message{From: "DAD <sender@example.org>", To: "donor@example.org", Subject: "Your gift — thank you", Body: "Line one\nA UTF-8 amount: €30\n.", ID: "<dad-login-fixture@example.org>"}
	wire, from, to, err := message.wire()
	if err != nil || from != "sender@example.org" || to != "donor@example.org" {
		t.Fatal("bad envelopes", err)
	}
	parsed, err := netmail.ReadMessage(strings.NewReader(string(wire)))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != message.Body {
		t.Fatal("body changed", string(body), err)
	}
	if parsed.Header.Get("Message-ID") != message.ID {
		t.Fatal("message identity changed")
	}
	for _, edit := range []func(*Message){func(m *Message) { m.To += "\r\nBcc: bad@example.org" }, func(m *Message) { m.From += "\nX: bad" }, func(m *Message) { m.Subject += "\r\nBcc: bad@example.org" }, func(m *Message) { m.ID += "\nX: bad" }} {
		bad := message
		edit(&bad)
		if _, _, _, err := bad.wire(); err == nil {
			t.Fatal("header injection allowed")
		}
	}
	id, err := MessageID(message.From, "login", "fixture")
	if err != nil || id != "<dad-login-fixture@example.org>" {
		t.Fatal(id, err)
	}
}

func TestSMTPRefusesCleartextAndHonorsCancellation(t *testing.T) {
	for _, mode := range []string{"no TLS", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			host, portText, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			connected := make(chan struct{})
			commands := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					commands <- "accept failed"
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				close(connected)
				if mode == "cancel" {
					_, err = io.Copy(io.Discard, conn)
					commands <- ""
					return
				}
				_, _ = io.WriteString(conn, "220 fixture SMTP\r\n")
				r := bufio.NewReader(conn)
				command, _ := r.ReadString('\n')
				if !strings.HasPrefix(command, "EHLO ") {
					commands <- command
					return
				}
				_, _ = io.WriteString(conn, "250 fixture\r\n")
				command, _ = r.ReadString('\n')
				if strings.HasPrefix(command, "STARTTLS") {
					_, _ = io.WriteString(conn, "502 unavailable\r\n")
					command, _ = r.ReadString('\n')
				}
				commands <- command
			}()
			transport := SMTPTransport{Settings: Settings{Host: host, Port: port, Username: "private-user", Password: "private-password", Sender: "sender@example.org", EncryptionKey: "configured"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- transport.Send(ctx, Message{From: "sender@example.org", To: "donor@example.org", Subject: "private subject", Body: "private body", ID: "<fixture@example.org>"})
			}()
			<-connected
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrDelivery) {
					t.Fatal("expected safe delivery failure", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("send ignored cancellation")
			}
			if command := <-commands; command != "" {
				t.Fatal("cleartext command escaped TLS boundary", command)
			}
		})
	}
}
