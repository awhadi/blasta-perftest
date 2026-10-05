// Package mailer sends plain-text email over SMTP.
package mailer

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config is an outgoing mail server.
type Config struct {
	Host     string
	Port     int
	Security string // starttls (default) | tls | none
	Username string
	Password string
	From     string
	// FromName is the sender's display name ("BLASTA by AWHADI"), used on every
	// message. The From address alone is shown when it is empty.
	FromName string
}

// Ready reports whether there is enough to send with.
func (c Config) Ready() bool { return c.Host != "" && c.From != "" }

// Validate checks the settings without connecting.
func (c Config) Validate() error {
	if c.Host == "" {
		return errors.New("the mail server host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("the mail server port must be between 1 and 65535")
	}
	switch c.Security {
	case "starttls", "tls", "none":
	default:
		return errors.New("security must be starttls, tls or none")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return errors.New("the From address is not a valid email address")
	}
	return nil
}

func clean(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// connect opens a session with the server: connection, greeting, STARTTLS if
// asked for, and the login. The caller closes it.
func connect(c Config) (*smtp.Client, error) {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if c.Security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot reach the mail server: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mail server did not greet us: %w", err)
	}
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			cl.Close()
			return nil, errors.New("the mail server does not offer STARTTLS; choose another security mode or fix the server")
		}
		if err := cl.StartTLS(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}); err != nil {
			cl.Close()
			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
	}
	if c.Username != "" {
		// PlainAuth refuses to send a password over an unencrypted connection.
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			cl.Close()
			return nil, fmt.Errorf("the mail server rejected the login: %w", err)
		}
	}
	return cl, nil
}

// Verify checks the server settings the way a real send would use them (reach the
// server, secure the connection, log in) and then hangs up without sending
// anything. It is the "test configuration" button, used before saving.
func Verify(c Config) (string, error) {
	switch {
	case c.Host == "":
		return "", errors.New("the mail server host is required")
	case c.Port < 1 || c.Port > 65535:
		return "", errors.New("the mail server port must be between 1 and 65535")
	case c.Security != "starttls" && c.Security != "tls" && c.Security != "none":
		return "", errors.New("security must be starttls, tls or none")
	}
	cl, err := connect(c)
	if err != nil {
		return "", err
	}
	defer cl.Close()
	_ = cl.Quit()
	msg := "Connected to " + net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	switch c.Security {
	case "tls", "starttls":
		msg += " with an encrypted connection"
	default:
		msg += " (not encrypted)"
	}
	if c.Username != "" {
		msg += " and the login was accepted"
	}
	return msg + ".", nil
}

// Send delivers one plain-text message.
func Send(c Config, to []string, subject, body string) error {
	return SendHTML(c, to, subject, body, "")
}

// SendHTML delivers one message with a plain-text part and, if html is not
// empty, an HTML alternative. Addresses and the subject are stripped of line
// breaks so nothing a user typed can add headers.
func SendHTML(c Config, to []string, subject, body, html string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	from, err := mail.ParseAddress(clean(c.From))
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(clean(c.FromName)); name != "" {
		from.Name = name // String() encodes a non-ASCII name properly
	}
	var rcpt []string
	for _, t := range to {
		a, err := mail.ParseAddress(clean(t))
		if err != nil {
			return fmt.Errorf("bad recipient %q", t)
		}
		rcpt = append(rcpt, a.Address)
	}
	if len(rcpt) == 0 {
		return errors.New("no recipients")
	}

	cl, err := connect(c)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("sender refused: %w", err)
	}
	for _, r := range rcpt {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("recipient %s refused: %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	var msg strings.Builder
	msg.WriteString("From: " + from.String() + "\r\n")
	msg.WriteString("To: " + strings.Join(rcpt, ", ") + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", clean(subject)) + "\r\n")
	msg.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	part := func(ctype, content string) {
		msg.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		enc := base64.StdEncoding.EncodeToString([]byte(content))
		for len(enc) > 76 {
			msg.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		msg.WriteString(enc + "\r\n")
	}
	if html == "" {
		part("text/plain", body)
	} else {
		var rb [12]byte
		_, _ = rand.Read(rb[:])
		boundary := "BLASTA-" + hex.EncodeToString(rb[:])
		msg.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		msg.WriteString("--" + boundary + "\r\n")
		part("text/plain", body)
		msg.WriteString("--" + boundary + "\r\n")
		part("text/html", html)
		msg.WriteString("--" + boundary + "--\r\n")
	}
	if _, err := w.Write([]byte(msg.String())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("message refused: %w", err)
	}
	return cl.Quit()
}
