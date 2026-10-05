package mailer

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal plain-text SMTP server that records the message.
func fakeSMTP(t *testing.T) (port int, got func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var data strings.Builder
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		say := func(s string) { c.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					say("250 queued")
					continue
				}
				mu.Lock()
				data.WriteString(line + "\n")
				mu.Unlock()
				continue
			}
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				mu.Lock()
				data.WriteString(line + "\n")
				mu.Unlock()
				say("250 ok")
			case cmd == "DATA":
				inData = true
				say("354 go")
			case cmd == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() string { mu.Lock(); defer mu.Unlock(); return data.String() }
}

func TestSendDeliversAMessage(t *testing.T) {
	port, got := fakeSMTP(t)
	err := Send(Config{Host: "127.0.0.1", Port: port, Security: "none", From: "BLASTA@x.test"},
		[]string{"pat@x.test"}, "Hello", "body text")
	if err != nil {
		t.Fatal(err)
	}
	m := got()
	for _, want := range []string{"MAIL FROM:<BLASTA@x.test>", "RCPT TO:<pat@x.test>", "Subject: Hello", "From: <BLASTA@x.test>"} {
		if !strings.Contains(m, want) {
			t.Errorf("message missing %q:\n%s", want, m)
		}
	}
}

// Nothing a user typed may add headers or recipients.
func TestSendBlocksHeaderInjection(t *testing.T) {
	port, got := fakeSMTP(t)
	err := Send(Config{Host: "127.0.0.1", Port: port, Security: "none", From: "BLASTA@x.test"},
		[]string{"pat@x.test"}, "Hi\r\nBcc: evil@x.test", "b")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got(), "\nBcc:") {
		t.Errorf("a line break in the subject must not create a header:\n%s", got())
	}
	if err := Send(Config{Host: "127.0.0.1", Port: port, Security: "none", From: "BLASTA@x.test"},
		[]string{"a@x.test\r\nRCPT TO:<evil@x.test>"}, "s", "b"); err == nil {
		t.Error("a malformed recipient must be refused")
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []Config{
		{}, {Host: "h", Port: 0, Security: "tls", From: "a@b.test"},
		{Host: "h", Port: 25, Security: "weird", From: "a@b.test"}, {Host: "h", Port: 25, Security: "tls", From: "nope"},
	} {
		if c.Validate() == nil {
			t.Errorf("%+v should be invalid", c)
		}
	}
	if err := (Config{Host: "h", Port: 587, Security: "starttls", From: "BLASTA <a@b.test>"}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestVerifyChecksTheServerWithoutSending(t *testing.T) {
	port, got := fakeSMTP(t)
	msg, err := Verify(Config{Host: "127.0.0.1", Port: port, Security: "none"})
	if err != nil || !strings.Contains(msg, "Connected to 127.0.0.1:") || !strings.Contains(msg, "not encrypted") {
		t.Fatalf("%q %v", msg, err)
	}
	if strings.Contains(got(), "MAIL FROM") {
		t.Error("verifying must not start a message")
	}
	for name, c := range map[string]Config{
		"no host":       {Port: 25, Security: "none"},
		"bad port":      {Host: "h", Port: 0, Security: "none"},
		"bad security":  {Host: "h", Port: 25, Security: "weird"},
		"nothing there": {Host: "127.0.0.1", Port: 1, Security: "none"},
	} {
		if _, err := Verify(c); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

// The sender text shows on every message, and nothing a person typed can add headers to it.
func TestSenderNameIsUsedOnTheFromHeader(t *testing.T) {
	port, got := fakeSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, Security: "none", From: "BLASTA@x.test", FromName: "BLASTA by AWHADI"}
	if err := Send(cfg, []string{"pat@x.test"}, "Hi", "body"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got(), `From: "BLASTA by AWHADI" <BLASTA@x.test>`) {
		t.Errorf("From header:\n%s", got())
	}
}

func TestSenderNameIsSafe(t *testing.T) {
	port, got := fakeSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, Security: "none", From: "BLASTA@x.test", FromName: "Évil\r\nBcc: x@evil.test"}
	if err := Send(cfg, []string{"pat@x.test"}, "Hi", "body"); err != nil {
		t.Fatal(err)
	}
	m := got()
	if strings.Contains(m, "\nBcc:") {
		t.Errorf("a line break in the sender name must not add a header:\n%s", m)
	}
	if !strings.Contains(m, "=?utf-8?") {
		t.Errorf("a non-ASCII name must be encoded:\n%s", m)
	}
}
