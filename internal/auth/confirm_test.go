package auth

import (
	"bufio"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/mailer"
)

// inbox is a tiny SMTP server that keeps every message it is given.
type inbox struct {
	mu   sync.Mutex
	msgs []string
	port int
}

func newInbox(t *testing.T) *inbox {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	in := &inbox{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				say := func(s string) { c.Write([]byte(s + "\r\n")) }
				say("220 fake")
				var data strings.Builder
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
							in.mu.Lock()
							in.msgs = append(in.msgs, data.String())
							in.mu.Unlock()
							data.Reset()
							say("250 queued")
							continue
						}
						data.WriteString(line + "\r\n")
						continue
					}
					switch cmd := strings.ToUpper(line); {
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
		}
	}()
	return in
}

func (in *inbox) config() mailer.Config {
	return mailer.Config{Host: "127.0.0.1", Port: in.port, Security: "none", From: "BLASTA@example.test"}
}

// wait returns the nth message once it arrives.
func (in *inbox) wait(t *testing.T, n int) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		in.mu.Lock()
		if len(in.msgs) >= n {
			m := in.msgs[n-1]
			in.mu.Unlock()
			return m
		}
		in.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("message %d never arrived", n)
	return ""
}

func (in *inbox) count() int { in.mu.Lock(); defer in.mu.Unlock(); return len(in.msgs) }

// parts decodes a multipart/alternative message into its text and html bodies.
func parts(t *testing.T, raw string) (subject, text, html string) {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subject, _ = dec.DecodeHeader(m.Header.Get("Subject"))
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("want multipart/alternative, got %q %v", mt, err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		raw, _ := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(b)))
		switch ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type")); ct {
		case "text/plain":
			text = string(raw)
		case "text/html":
			html = string(raw)
		}
	}
	return
}

var tokenRe = regexp.MustCompile(`#/(confirm|reset)\?token=([A-Za-z0-9_-]+)`)

func TestWithEmailNewAccountsMustConfirmFirst(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{Registration: RegOpen, PublicURL: "https://blasta.example.test", SMTP: in.config()})
	if _, err := s.Register("admin@x.test", "Admin", goodPW, "", "1.1.1.1"); err != nil {
		t.Fatal(err) // the first account is the administrator and needs no email
	}
	u, err := s.Register("new@x.test", "Nia", goodPW, "", "1.1.1.2")
	if err != nil || u.Status != StatusUnverified {
		t.Fatalf("with email set up a new account is unverified until confirmed: %+v %v", u, err)
	}
	if _, _, err := s.Login("new@x.test", goodPW, "1.1.1.2"); err != ErrUnverified {
		t.Errorf("signing in before confirming must say why, got %v", err)
	}

	if !strings.Contains(in.wait(t, 1), "From: \"BLASTA by AWHADI\" <BLASTA@example.test>") {
		t.Errorf("every email carries the sender text by default:\n%s", in.wait(t, 1))
	}
	subject, text, html := parts(t, in.wait(t, 1))
	if subject != "Confirm your email for BLASTA" {
		t.Errorf("subject = %q", subject)
	}
	m := tokenRe.FindStringSubmatch(text)
	if m == nil || m[1] != "confirm" || !strings.Contains(text, "https://blasta.example.test/#/confirm?token=") {
		t.Fatalf("the plain-text part must carry the link: %q", text)
	}
	// It is in BLASTA's look: the orange button, the wordmark, a dark-mode variant.
	for _, want := range []string{"#e8663a", "blasta", "by AWHADI", "prefers-color-scheme: dark", "Confirm my email", "href=\"https://blasta.example.test/#/confirm?token="} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML email is missing %q", want)
		}
	}

	got, session, err := s.ConfirmEmail(m[2])
	if err != nil || got.Status != StatusActive || session == "" {
		t.Fatalf("confirming activates the account and signs them in: %+v %q %v", got, session, err)
	}
	if _, err := s.Authenticate(session); err != nil {
		t.Errorf("the session from confirming must work: %v", err)
	}
	if _, _, err := s.ConfirmEmail(m[2]); err == nil {
		t.Error("a confirmation link works once")
	}
	if _, _, err := s.Login("new@x.test", goodPW, "1.1.1.2"); err != nil {
		t.Errorf("after confirming, sign-in works: %v", err)
	}
}

func TestConfirmingThenWaitsForApprovalWhenRequired(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{Registration: RegApproval, PublicURL: "https://b.example.test", SMTP: in.config()})
	s.Register("admin@x.test", "Admin", goodPW, "", "1.1.1.1")
	s.Register("new@x.test", "Nia", goodPW, "", "1.1.1.2")
	_, text, _ := parts(t, in.wait(t, 1))
	got, session, err := s.ConfirmEmail(tokenRe.FindStringSubmatch(text)[2])
	if err != nil || got.Status != StatusPending || session != "" {
		t.Fatalf("with approval required, confirming leaves the account pending: %+v %q %v", got, session, err)
	}
	// ...and the administrator is told (second message), in the same look.
	subject, _, html := parts(t, in.wait(t, 2))
	if !strings.Contains(subject, "waiting for approval") || !strings.Contains(html, "#e8663a") {
		t.Errorf("admin notice: %q", subject)
	}
}

func TestWithoutEmailRegistrationActivatesAtOnce(t *testing.T) {
	s := newSvc(t, Config{Registration: RegOpen})
	s.Register("admin@x.test", "Admin", goodPW, "", "1.1.1.1")
	u, err := s.Register("new@x.test", "Nia", goodPW, "", "1.1.1.2")
	if err != nil || u.Status != StatusActive {
		t.Fatalf("without email there is nothing to confirm: %+v %v", u, err)
	}
	if _, _, err := s.Login("new@x.test", goodPW, "1.1.1.2"); err != nil {
		t.Error(err)
	}
	// Registration can be switched off.
	s2 := newSvc(t, Config{Registration: RegClosed})
	s2.Register("admin@x.test", "Admin", goodPW, "", "1.1.1.1")
	if _, err := s2.Register("late@x.test", "L", goodPW, "", "1.1.1.3"); err != ErrRegistrationClosed {
		t.Errorf("disabled registration must refuse, got %v", err)
	}
	if s.PublicConfig().ConfirmEmail {
		t.Error("no email: nothing to confirm")
	}
}

func TestResendAndExpiry(t *testing.T) {
	in := newInbox(t)
	s := newSvc(t, Config{Registration: RegOpen, PublicURL: "https://b.example.test", SMTP: in.config()})
	s.Register("admin@x.test", "Admin", goodPW, "", "1.1.1.1")
	s.Register("new@x.test", "Nia", goodPW, "", "1.1.1.2")
	_, first, _ := parts(t, in.wait(t, 1))
	if err := s.ResendConfirmation("https://b.example.test", "new@x.test", "1.1.1.2"); err != nil {
		t.Fatal(err)
	}
	_, second, _ := parts(t, in.wait(t, 2))
	if tokenRe.FindStringSubmatch(first)[2] == tokenRe.FindStringSubmatch(second)[2] {
		t.Error("a resend must make a new link")
	}
	if _, _, err := s.ConfirmEmail(tokenRe.FindStringSubmatch(first)[2]); err == nil {
		t.Error("the old link must stop working once a new one is sent")
	}
	// Asking about an unknown or already-confirmed address says nothing and sends nothing.
	n := in.count()
	s.ResendConfirmation("https://b.example.test", "ghost@x.test", "1.1.1.2")
	s.ResendConfirmation("https://b.example.test", "admin@x.test", "1.1.1.2")
	time.Sleep(100 * time.Millisecond)
	if in.count() != n {
		t.Error("no email may go to an address that is not waiting for confirmation")
	}
	// Expired links are refused.
	u := s.store.UserByEmail("new@x.test")
	tok, _ := randomToken(32)
	s.store.PutConfirmToken(tokenHash(tok), u.ID, s.now().Add(-time.Minute))
	if _, _, err := s.ConfirmEmail(tok); err == nil {
		t.Error("an expired link must be refused")
	}
	// Without mail, resend explains.
	if err := newSvc(t, Config{}).ResendConfirmation("x", "a@b.test", "1.1.1.1"); err != ErrMailOff {
		t.Errorf("resend without email = %v", err)
	}
}

// Anything a person typed is escaped in the HTML email.
func TestThemedMailEscapesInput(t *testing.T) {
	_, html := themed("Hi", []string{`<script>alert(1)</script> & "quotes"`}, "Go", `https://x.test/?a=1&b="2"`, "foot<b>")
	if strings.Contains(html, "<script>") || strings.Contains(html, "<b>") || strings.Contains(html, `b="2"`) {
		t.Errorf("unescaped input in the HTML email:\n%s", html)
	}
}
