package mail

import (
	"testing"
)

func TestEnabled(t *testing.T) {
	t.Run("disabled when host empty", func(t *testing.T) {
		svc := NewService(Config{})
		if svc.Enabled() {
			t.Fatal("expected disabled when host is empty")
		}
	})

	t.Run("enabled when host set", func(t *testing.T) {
		svc := NewService(Config{Host: "smtp.example.com", Port: "587"})
		if !svc.Enabled() {
			t.Fatal("expected enabled when host is set")
		}
	})
}

func TestSendDisabled(t *testing.T) {
	svc := NewService(Config{})
	if err := svc.Send("user@example.com", "test", "body"); err != nil {
		t.Fatalf("Send on disabled service should return nil, got: %v", err)
	}
}

func TestSendPasswordResetDisabled(t *testing.T) {
	svc := NewService(Config{AppName: "TestApp"})
	if err := svc.SendPasswordReset("user@example.com", "https://example.com", "tok123"); err != nil {
		t.Fatalf("SendPasswordReset on disabled service should return nil, got: %v", err)
	}
}

func TestSendVerificationDisabled(t *testing.T) {
	svc := NewService(Config{AppName: "TestApp"})
	if err := svc.SendVerification("user@example.com", "https://example.com", "tok456"); err != nil {
		t.Fatalf("SendVerification on disabled service should return nil, got: %v", err)
	}
}

func TestNewService(t *testing.T) {
	cfg := Config{
		Host:     "smtp.example.com",
		Port:     "587",
		Username: "user",
		Password: "pass",
		From:     "noreply@example.com",
		AppName:  "MyApp",
	}
	svc := NewService(cfg)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	if svc.cfg.AppName != "MyApp" {
		t.Fatalf("expected AppName MyApp, got %s", svc.cfg.AppName)
	}
}

func TestLoginAuth(t *testing.T) {
	a := &loginAuth{username: "user@test.com", password: "secret"}

	proto, data, err := a.Start(nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if proto != "LOGIN" {
		t.Fatalf("expected LOGIN, got %s", proto)
	}
	if data != nil {
		t.Fatalf("expected nil data, got %v", data)
	}

	resp, err := a.Next([]byte("Username:"), true)
	if err != nil {
		t.Fatalf("Next Username: %v", err)
	}
	if string(resp) != "user@test.com" {
		t.Fatalf("expected user@test.com, got %s", resp)
	}

	resp, err = a.Next([]byte("Password:"), true)
	if err != nil {
		t.Fatalf("Next Password: %v", err)
	}
	if string(resp) != "secret" {
		t.Fatalf("expected secret, got %s", resp)
	}

	resp, err = a.Next(nil, false)
	if err != nil {
		t.Fatalf("Next more=false: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil, got %v", resp)
	}

	_, err = a.Next([]byte("Unknown:"), true)
	if err == nil {
		t.Fatal("expected error for unknown challenge")
	}
}
