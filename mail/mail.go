// CLAUDE:SUMMARY Shared SMTP mail service: STARTTLS + LOGIN auth (OVH), password reset, verification emails.
// CLAUDE:DEPENDS log/slog, net/smtp, crypto/tls
// CLAUDE:EXPORTS Config, Service, NewService
package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
)

// Config holds SMTP connection parameters.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	AppName  string // Used in email subjects/bodies (e.g. "SiftRAG", "repvow")
}

// Service sends emails via SMTP with STARTTLS and LOGIN auth.
type Service struct {
	cfg Config
}

// NewService creates a mail service. If Host is empty, the service is disabled
// and Send calls will log a warning and return nil.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// Enabled returns true if SMTP host is configured.
func (s *Service) Enabled() bool {
	return s.cfg.Host != ""
}

// Send delivers a plain-text email via SMTP STARTTLS with LOGIN auth.
func (s *Service) Send(to, subject, body string) error {
	if !s.Enabled() {
		slog.Warn("mail: SMTP disabled, skipping", "to", to, "subject", subject)
		return nil
	}

	encodedSubject := mime.QEncoding.Encode("utf-8", subject)
	msg := strings.Join([]string{
		"From: " + s.cfg.From,
		"To: " + to,
		"Subject: " + encodedSubject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Close()

	if err = c.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}

	tlsCfg := &tls.Config{ServerName: s.cfg.Host}
	if err = c.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}

	if err = c.Auth(&loginAuth{username: s.cfg.Username, password: s.cfg.Password}); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err = c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}

	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}

	slog.Info("mail sent", "to", to, "subject", subject)
	return nil
}

// SendPasswordReset sends a password reset email with a tokenized link.
func (s *Service) SendPasswordReset(to, baseURL, token string) error {
	link := baseURL + "/reset-password?token=" + token
	body := fmt.Sprintf(
		"Vous avez demandé la réinitialisation de votre mot de passe sur %s.\n\n"+
			"Cliquez sur ce lien pour choisir un nouveau mot de passe :\n%s\n\n"+
			"Ce lien expire dans 1 heure.\n\n"+
			"Si vous n'êtes pas à l'origine de cette demande, ignorez cet email.",
		s.cfg.AppName, link,
	)
	return s.Send(to, fmt.Sprintf("Réinitialisation de votre mot de passe - %s", s.cfg.AppName), body)
}

// SendVerification sends an email verification link.
func (s *Service) SendVerification(to, baseURL, token string) error {
	link := baseURL + "/verify-email?token=" + token
	body := fmt.Sprintf(
		"Bienvenue sur %s !\n\nCliquez sur ce lien pour vérifier votre email :\n%s\n\nCe lien expire dans 24 heures.",
		s.cfg.AppName, link,
	)
	return s.Send(to, fmt.Sprintf("Vérification de votre email - %s", s.cfg.AppName), body)
}

// loginAuth implements smtp.Auth using the LOGIN mechanism (required by OVH).
type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch string(fromServer) {
	case "Username:":
		return []byte(a.username), nil
	case "Password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected server challenge: %s", fromServer)
	}
}
