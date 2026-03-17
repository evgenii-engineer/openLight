package accounts

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"openlight/internal/config"
	"openlight/internal/skills"
	serviceskills "openlight/internal/skills/services"
)

type ProviderInfo struct {
	Name      string
	Service   string
	CanAdd    bool
	CanDelete bool
	CanList   bool
}

type UserListResult struct {
	Provider string
	Output   string
}

type Manager interface {
	ListProviders() []ProviderInfo
	ListUsers(ctx context.Context, provider, pattern string) (UserListResult, error)
	AddUser(ctx context.Context, provider, username, password string) error
	DeleteUser(ctx context.Context, provider, username string) error
}

type AccountManager struct {
	providers map[string]config.AccountProviderConfig
	services  serviceskills.Manager
}

func NewManager(providers map[string]config.AccountProviderConfig, services serviceskills.Manager) (Manager, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	if services == nil {
		return nil, fmt.Errorf("services manager is required")
	}

	normalized := make(map[string]config.AccountProviderConfig, len(providers))
	for name, provider := range providers {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		if normalizedName == "" {
			return nil, fmt.Errorf("account provider name is required")
		}
		if strings.TrimSpace(provider.Service) == "" {
			return nil, fmt.Errorf("account provider %q service is required", normalizedName)
		}
		normalized[normalizedName] = provider
	}

	return &AccountManager{
		providers: normalized,
		services:  services,
	}, nil
}

func (m *AccountManager) ListProviders() []ProviderInfo {
	if len(m.providers) == 0 {
		return nil
	}

	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]ProviderInfo, 0, len(names))
	for _, name := range names {
		provider := m.providers[name]
		result = append(result, ProviderInfo{
			Name:      name,
			Service:   provider.Service,
			CanAdd:    len(provider.AddCommand) > 0,
			CanDelete: len(provider.DeleteCommand) > 0,
			CanList:   len(provider.ListCommand) > 0,
		})
	}
	return result
}

func (m *AccountManager) ListUsers(ctx context.Context, provider, pattern string) (UserListResult, error) {
	providerName, cfg, fallbackPattern, err := m.resolveProviderOrSinglePattern(provider)
	if err != nil {
		return UserListResult{}, err
	}
	pattern = strings.TrimSpace(strings.Join([]string{fallbackPattern, strings.TrimSpace(pattern)}, " "))
	if len(cfg.ListCommand) == 0 {
		return UserListResult{}, fmt.Errorf("%w: listing users is not configured for %s", skills.ErrAccessDenied, providerName)
	}

	command := renderCommand(cfg.ListCommand, map[string]string{
		"provider": providerName,
		"service":  cfg.Service,
		"pattern":  pattern,
	})
	output, err := m.services.Exec(ctx, cfg.Service, command...)
	if err != nil {
		return UserListResult{}, err
	}

	return UserListResult{
		Provider: providerName,
		Output:   strings.TrimSpace(output),
	}, nil
}

func (m *AccountManager) AddUser(ctx context.Context, provider, username, password string) error {
	providerName, cfg, err := m.resolveProvider(provider)
	if err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" {
		return fmt.Errorf("%w: username is required", skills.ErrInvalidArguments)
	}
	if password == "" {
		return fmt.Errorf("%w: password is required", skills.ErrInvalidArguments)
	}
	if len(cfg.AddCommand) == 0 {
		return fmt.Errorf("%w: adding users is not configured for %s", skills.ErrAccessDenied, providerName)
	}

	command := renderCommand(cfg.AddCommand, map[string]string{
		"provider": providerName,
		"service":  cfg.Service,
		"username": username,
		"password": password,
	})
	_, err = m.services.Exec(ctx, cfg.Service, command...)
	return err
}

func (m *AccountManager) DeleteUser(ctx context.Context, provider, username string) error {
	providerName, cfg, err := m.resolveProvider(provider)
	if err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("%w: username is required", skills.ErrInvalidArguments)
	}
	if len(cfg.DeleteCommand) == 0 {
		return fmt.Errorf("%w: deleting users is not configured for %s", skills.ErrAccessDenied, providerName)
	}

	command := renderCommand(cfg.DeleteCommand, map[string]string{
		"provider": providerName,
		"service":  cfg.Service,
		"username": username,
	})
	_, err = m.services.Exec(ctx, cfg.Service, command...)
	return err
}

func (m *AccountManager) resolveProvider(provider string) (string, config.AccountProviderConfig, error) {
	if len(m.providers) == 0 {
		return "", config.AccountProviderConfig{}, fmt.Errorf("%w: no account providers are configured", skills.ErrUnavailable)
	}

	name := strings.ToLower(strings.TrimSpace(provider))
	if name != "" {
		cfg, ok := m.providers[name]
		if !ok {
			return "", config.AccountProviderConfig{}, fmt.Errorf("%w: unknown provider %s", skills.ErrAccessDenied, name)
		}
		return name, cfg, nil
	}

	if len(m.providers) == 1 {
		for singleName, cfg := range m.providers {
			return singleName, cfg, nil
		}
	}

	return "", config.AccountProviderConfig{}, fmt.Errorf("%w: provider is required", skills.ErrInvalidArguments)
}

func (m *AccountManager) resolveProviderOrSinglePattern(provider string) (string, config.AccountProviderConfig, string, error) {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" {
		resolvedName, cfg, err := m.resolveProvider("")
		return resolvedName, cfg, "", err
	}

	if cfg, ok := m.providers[name]; ok {
		return name, cfg, "", nil
	}

	if len(m.providers) == 1 {
		for singleName, cfg := range m.providers {
			return singleName, cfg, strings.TrimSpace(provider), nil
		}
	}

	return "", config.AccountProviderConfig{}, "", fmt.Errorf("%w: unknown provider %s", skills.ErrAccessDenied, name)
}

func renderCommand(template []string, values map[string]string) []string {
	if len(template) == 0 {
		return nil
	}

	replacements := make([]string, 0, len(values)*2)
	for key, value := range values {
		replacements = append(replacements, "{"+key+"}", value)
	}
	replacer := strings.NewReplacer(replacements...)

	result := make([]string, 0, len(template))
	for _, part := range template {
		rendered := strings.TrimSpace(replacer.Replace(part))
		if rendered != "" {
			result = append(result, rendered)
		}
	}
	return result
}
