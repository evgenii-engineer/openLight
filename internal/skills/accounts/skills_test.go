package accounts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"openlight/internal/config"
	"openlight/internal/skills"
	serviceskills "openlight/internal/skills/services"
)

type stubServicesManager struct {
	lastService string
	lastArgs    []string
	execErr     error
}

func (s *stubServicesManager) List(context.Context) ([]serviceskills.Info, error) {
	return nil, nil
}

func (s *stubServicesManager) Status(context.Context, string) (serviceskills.Info, error) {
	return serviceskills.Info{}, nil
}

func (s *stubServicesManager) Restart(context.Context, string) error {
	return nil
}

func (s *stubServicesManager) Logs(context.Context, string, int) (string, error) {
	return "", nil
}

func (s *stubServicesManager) Exec(_ context.Context, service string, args ...string) (string, error) {
	s.lastService = service
	s.lastArgs = append([]string(nil), args...)
	return "", s.execErr
}

func TestAccountManagerAddUserRendersTemplate(t *testing.T) {
	t.Parallel()

	services := &stubServicesManager{}
	manager, err := NewManager(map[string]config.AccountProviderConfig{
		"jitsi": {
			Service: "jitsi-prosody",
			AddCommand: []string{
				"prosodyctl",
				"--config",
				"/config/prosody.cfg.lua",
				"register",
				"{username}",
				"meet.jitsi",
				"{password}",
			},
		},
	}, services)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := manager.AddUser(context.Background(), "jitsi", "anya", "123456"); err != nil {
		t.Fatalf("AddUser returned error: %v", err)
	}

	if services.lastService != "jitsi-prosody" {
		t.Fatalf("unexpected service: %q", services.lastService)
	}
	wantArgs := []string{"prosodyctl", "--config", "/config/prosody.cfg.lua", "register", "anya", "meet.jitsi", "123456"}
	if !reflect.DeepEqual(services.lastArgs, wantArgs) {
		t.Fatalf("unexpected exec args: %#v", services.lastArgs)
	}
}

func TestAccountManagerDeleteUserUsesSingleProviderImplicitly(t *testing.T) {
	t.Parallel()

	services := &stubServicesManager{}
	manager, err := NewManager(map[string]config.AccountProviderConfig{
		"jitsi": {
			Service:       "jitsi-prosody",
			DeleteCommand: []string{"prosodyctl", "unregister", "{username}", "meet.jitsi"},
		},
	}, services)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := manager.DeleteUser(context.Background(), "", "anya"); err != nil {
		t.Fatalf("DeleteUser returned error: %v", err)
	}

	wantArgs := []string{"prosodyctl", "unregister", "anya", "meet.jitsi"}
	if !reflect.DeepEqual(services.lastArgs, wantArgs) {
		t.Fatalf("unexpected exec args: %#v", services.lastArgs)
	}
}

func TestAccountManagerListUsersUsesPatternFallbackForSingleProvider(t *testing.T) {
	t.Parallel()

	services := &stubServicesManager{}
	manager, err := NewManager(map[string]config.AccountProviderConfig{
		"jitsi": {
			Service:     "jitsi-prosody",
			ListCommand: []string{"prosodyctl", "shell", "user", "list", "meet.jitsi", "{pattern}"},
		},
	}, services)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	result, err := manager.ListUsers(context.Background(), "anya", "")
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}

	if result.Provider != "jitsi" {
		t.Fatalf("unexpected provider result: %#v", result)
	}
	wantArgs := []string{"prosodyctl", "shell", "user", "list", "meet.jitsi", "anya"}
	if !reflect.DeepEqual(services.lastArgs, wantArgs) {
		t.Fatalf("unexpected exec args: %#v", services.lastArgs)
	}
}

func TestAccountManagerRequiresProviderWhenMultipleConfigured(t *testing.T) {
	t.Parallel()

	services := &stubServicesManager{}
	manager, err := NewManager(map[string]config.AccountProviderConfig{
		"jitsi":  {Service: "jitsi-prosody", AddCommand: []string{"add"}},
		"matrix": {Service: "synapse-admin", AddCommand: []string{"add"}},
	}, services)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	err = manager.AddUser(context.Background(), "", "anya", "123456")
	if !errors.Is(err, skills.ErrInvalidArguments) {
		t.Fatalf("expected invalid arguments, got %v", err)
	}
}

func TestAccountSkillsFormatResponses(t *testing.T) {
	t.Parallel()

	services := &stubServicesManager{}
	manager, err := NewManager(map[string]config.AccountProviderConfig{
		"jitsi": {
			Service:       "jitsi-prosody",
			AddCommand:    []string{"add"},
			DeleteCommand: []string{"delete"},
			ListCommand:   []string{"list"},
		},
	}, services)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	result, err := NewProvidersSkill(manager).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("providers Execute returned error: %v", err)
	}
	if want := "Configured account providers:\n- jitsi: add, delete, list via jitsi-prosody"; result.Text != want {
		t.Fatalf("unexpected providers output: %q", result.Text)
	}

	result, err = NewListUserSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"provider": "jitsi"},
	})
	if err != nil {
		t.Fatalf("list Execute returned error: %v", err)
	}
	if want := "No users found (jitsi)."; result.Text != want {
		t.Fatalf("unexpected list output: %q", result.Text)
	}

	result, err = NewAddUserSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"provider": "jitsi", "username": "anya", "password": "123456"},
	})
	if err != nil {
		t.Fatalf("add Execute returned error: %v", err)
	}
	if want := "User added: anya (jitsi)"; result.Text != want {
		t.Fatalf("unexpected add output: %q", result.Text)
	}

	result, err = NewDeleteUserSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"provider": "jitsi", "username": "anya"},
	})
	if err != nil {
		t.Fatalf("delete Execute returned error: %v", err)
	}
	if want := "User deleted: anya (jitsi)"; result.Text != want {
		t.Fatalf("unexpected delete output: %q", result.Text)
	}
}
