// Package mail delivers sandbox account and financial messages over verified TLS.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

var ErrDelivery = errors.New("email delivery failed")
var ErrConfiguration = errors.New("invalid email configuration")

type Settings struct {
	Host                                      string
	Port                                      int
	Username, Password, Sender, EncryptionKey string
}
type Message struct{ From, To, Subject, Body, ID string }
type Transport interface {
	Send(context.Context, Message) error
}

func address(value string) (*netmail.Address, error) {
	if strings.ContainsAny(value, "\r\n") {
		return nil, ErrConfiguration
	}
	parsed, err := netmail.ParseAddress(value)
	if err != nil || !strings.Contains(parsed.Address, "@") {
		return nil, ErrConfiguration
	}
	return parsed, nil
}
func (s Settings) Validate() error {
	if s.Host == "" || strings.ContainsAny(s.Host, "\r\n") || s.Port < 1 || s.Port > 65535 || s.EncryptionKey == "" {
		return ErrConfiguration
	}
	_, err := address(s.Sender)
	return err
}
func MessageID(sender, kind, id string) (string, error) {
	from, err := address(sender)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(kind+id, "\r\n<>@ \t") {
		return "", ErrConfiguration
	}
	_, domain, _ := strings.Cut(from.Address, "@")
	return "<dad-" + kind + "-" + id + "@" + domain + ">", nil
}
func (m Message) wire() ([]byte, string, string, error) {
	from, err := address(m.From)
	if err != nil {
		return nil, "", "", err
	}
	to, err := address(m.To)
	if err != nil {
		return nil, "", "", err
	}
	if strings.ContainsAny(m.Subject+m.ID, "\r\n") || !strings.HasPrefix(m.ID, "<") || !strings.HasSuffix(m.ID, ">") {
		return nil, "", "", ErrConfiguration
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", from.String(), to.String(), mime.QEncoding.Encode("utf-8", m.Subject), m.ID)
	writer := quotedprintable.NewWriter(&b)
	if _, err = writer.Write([]byte(m.Body)); err != nil {
		return nil, "", "", err
	}
	if err = writer.Close(); err != nil {
		return nil, "", "", err
	}
	return b.Bytes(), from.Address, to.Address, nil
}

type SMTPTransport struct{ Settings Settings }

func (s SMTPTransport) Send(ctx context.Context, message Message) error {
	if err := s.Settings.Validate(); err != nil {
		return err
	}
	wire, from, to, err := message.wire()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.Settings.Host, strconv.Itoa(s.Settings.Port)))
	if err != nil {
		return ErrDelivery
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err = connection.SetDeadline(deadline); err != nil {
			return ErrDelivery
		}
	}
	client, err := smtp.NewClient(connection, s.Settings.Host)
	if err != nil {
		return ErrDelivery
	}
	defer client.Close()
	// STARTTLS is mandatory; never fall back to cleartext credentials or content.
	if err = client.StartTLS(&tls.Config{ServerName: s.Settings.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return ErrDelivery
	}
	if s.Settings.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Settings.Username, s.Settings.Password, s.Settings.Host)); err != nil {
			return ErrDelivery
		}
	}
	if err = client.Mail(from); err != nil {
		return ErrDelivery
	}
	if err = client.Rcpt(to); err != nil {
		return ErrDelivery
	}
	data, err := client.Data()
	if err != nil {
		return ErrDelivery
	}
	if _, err = data.Write(wire); err != nil {
		return ErrDelivery
	}
	if err = data.Close(); err != nil {
		return ErrDelivery
	}
	if err = client.Quit(); err != nil {
		return ErrDelivery
	}
	return nil
}
