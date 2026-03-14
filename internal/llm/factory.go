package llm

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ProviderGeneric = "generic"
	ProviderOllama  = "ollama"
	ProviderOpenAI  = "openai"
)

type ProviderConfig struct {
	Endpoint string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

type ProviderFactory interface {
	Name() string
	Build(cfg ProviderConfig, logger *slog.Logger) (Provider, error)
}

type FactoryRegistry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

var defaultFactoryRegistry = func() *FactoryRegistry {
	registry := NewFactoryRegistry()
	MustRegisterBuiltInProviderFactories(registry)
	return registry
}()

func NewFactoryRegistry(factories ...ProviderFactory) *FactoryRegistry {
	registry := &FactoryRegistry{
		factories: make(map[string]ProviderFactory),
	}
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		registry.MustRegister(factory)
	}
	return registry
}

func DefaultFactoryRegistry() *FactoryRegistry {
	return defaultFactoryRegistry.Clone()
}

func BuildProvider(name string, cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	return defaultFactoryRegistry.Build(name, cfg, logger)
}

func RegisterDefaultProviderFactory(factory ProviderFactory) error {
	return defaultFactoryRegistry.Register(factory)
}

func MustRegisterDefaultProviderFactory(factory ProviderFactory) {
	defaultFactoryRegistry.MustRegister(factory)
}

func DefaultProviderNames() []string {
	return defaultFactoryRegistry.Names()
}

func RegisterBuiltInProviderFactories(registry *FactoryRegistry) error {
	if registry == nil {
		return fmt.Errorf("factory registry is required")
	}

	for _, factory := range BuiltInProviderFactories() {
		if err := registry.Register(factory); err != nil {
			return err
		}
	}

	return nil
}

func MustRegisterBuiltInProviderFactories(registry *FactoryRegistry) {
	if err := RegisterBuiltInProviderFactories(registry); err != nil {
		panic(err)
	}
}

func BuiltInProviderFactories() []ProviderFactory {
	return []ProviderFactory{
		NewHTTPProviderFactory(),
		NewOllamaProviderFactory(),
		NewOpenAIProviderFactory(),
	}
}

func NewProviderFactory(name string, build func(cfg ProviderConfig, logger *slog.Logger) (Provider, error)) ProviderFactory {
	return providerFactory{
		name:  name,
		build: build,
	}
}

func NewHTTPProviderFactory() ProviderFactory {
	return NewProviderFactory(ProviderGeneric, buildGenericProvider)
}

func NewOllamaProviderFactory() ProviderFactory {
	return NewProviderFactory(ProviderOllama, buildOllamaProvider)
}

func NewOpenAIProviderFactory() ProviderFactory {
	return NewProviderFactory(ProviderOpenAI, buildOpenAIProvider)
}

func (r *FactoryRegistry) Register(factory ProviderFactory) error {
	if factory == nil {
		return fmt.Errorf("provider factory is required")
	}

	name := normalizeFactoryName(factory.Name())
	if name == "" {
		return fmt.Errorf("provider factory name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("provider factory %q already registered", name)
	}

	r.factories[name] = factory
	return nil
}

func (r *FactoryRegistry) MustRegister(factory ProviderFactory) {
	if err := r.Register(factory); err != nil {
		panic(err)
	}
}

func (r *FactoryRegistry) Build(name string, cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	key := normalizeFactoryName(name)

	r.mu.RLock()
	factory, ok := r.factories[key]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown llm provider: %s", name)
	}

	return factory.Build(cfg, logger)
}

func (r *FactoryRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.factories))
	for name := range r.factories {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (r *FactoryRegistry) Clone() *FactoryRegistry {
	clone := NewFactoryRegistry()

	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, factory := range r.factories {
		clone.factories[name] = factory
	}

	return clone
}

type providerFactory struct {
	name  string
	build func(cfg ProviderConfig, logger *slog.Logger) (Provider, error)
}

func (f providerFactory) Name() string {
	return f.name
}

func (f providerFactory) Build(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	if f.build == nil {
		return nil, fmt.Errorf("provider factory %q is not configured", f.name)
	}
	return f.build(cfg, logger)
}

func buildGenericProvider(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("llm.endpoint is required for provider %q", ProviderGeneric)
	}
	return NewHTTPProvider(cfg.Endpoint, cfg.Timeout, logger), nil
}

func buildOllamaProvider(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("llm.endpoint is required for provider %q", ProviderOllama)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm.model is required for provider %q", ProviderOllama)
	}
	return NewOllamaProvider(cfg.Endpoint, cfg.Model, cfg.Timeout, logger), nil
}

func buildOpenAIProvider(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("llm.endpoint is required for provider %q", ProviderOpenAI)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm.model is required for provider %q", ProviderOpenAI)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("llm.api_key is required for provider %q", ProviderOpenAI)
	}
	return NewOpenAIProvider(cfg.Endpoint, cfg.Model, cfg.APIKey, cfg.Timeout, logger), nil
}

func normalizeFactoryName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
