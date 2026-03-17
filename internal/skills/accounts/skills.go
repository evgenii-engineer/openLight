package accounts

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

type providersSkill struct {
	manager Manager
}

func NewProvidersSkill(manager Manager) skills.Skill {
	return &providersSkill{manager: manager}
}

func (s *providersSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "user_providers",
		Group:       skills.GroupAccounts,
		Description: "List configured account providers and the operations they allow.",
		Aliases:     []string{"users", "accounts", "user providers", "account providers"},
		Usage:       "/users",
	}
}

func (s *providersSkill) Execute(_ context.Context, _ skills.Input) (skills.Result, error) {
	providers := s.manager.ListProviders()
	if len(providers) == 0 {
		return skills.Result{Text: "No account providers are configured."}, nil
	}

	lines := make([]string, 0, len(providers))
	for _, provider := range providers {
		operations := make([]string, 0, 2)
		if provider.CanAdd {
			operations = append(operations, "add")
		}
		if provider.CanDelete {
			operations = append(operations, "delete")
		}
		if provider.CanList {
			operations = append(operations, "list")
		}
		if len(operations) == 0 {
			operations = append(operations, "none")
		}

		lines = append(lines, fmt.Sprintf("- %s: %s via %s", provider.Name, strings.Join(operations, ", "), provider.Service))
	}

	return skills.Result{Text: "Configured account providers:\n" + strings.Join(lines, "\n")}, nil
}

type addUserSkill struct {
	manager Manager
}

func NewAddUserSkill(manager Manager) skills.Skill {
	return &addUserSkill{manager: manager}
}

func (s *addUserSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "user_add",
		Group:       skills.GroupAccounts,
		Description: "Create a user through one configured account provider.",
		Aliases:     []string{"user add", "add user", "register user"},
		Usage:       "/user_add [provider] <username> <password>",
		Examples: []string{
			"user_add jitsi anya 123456",
			"user add anya 123456",
		},
		Mutating: true,
	}
}

func (s *addUserSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	provider := strings.TrimSpace(input.Args["provider"])
	username := strings.TrimSpace(input.Args["username"])
	password := strings.TrimSpace(input.Args["password"])

	if err := s.manager.AddUser(ctx, provider, username, password); err != nil {
		return skills.Result{}, err
	}

	if provider == "" {
		return skills.Result{Text: "User added: " + username}, nil
	}
	return skills.Result{Text: fmt.Sprintf("User added: %s (%s)", username, provider)}, nil
}

type listUserSkill struct {
	manager Manager
}

func NewListUserSkill(manager Manager) skills.Skill {
	return &listUserSkill{manager: manager}
}

func (s *listUserSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "user_list",
		Group:       skills.GroupAccounts,
		Description: "List users through one configured account provider.",
		Aliases:     []string{"user list", "list users"},
		Usage:       "/user_list [provider] [pattern]",
		Examples: []string{
			"user_list jitsi",
			"user list jitsi anya",
			"user list",
		},
	}
}

func (s *listUserSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	provider := strings.TrimSpace(input.Args["provider"])
	pattern := strings.TrimSpace(input.Args["pattern"])

	result, err := s.manager.ListUsers(ctx, provider, pattern)
	if err != nil {
		return skills.Result{}, err
	}
	if result.Output == "" {
		return skills.Result{Text: fmt.Sprintf("No users found (%s).", result.Provider)}, nil
	}

	return skills.Result{Text: fmt.Sprintf("Users (%s):\n%s", result.Provider, result.Output)}, nil
}

type deleteUserSkill struct {
	manager Manager
}

func NewDeleteUserSkill(manager Manager) skills.Skill {
	return &deleteUserSkill{manager: manager}
}

func (s *deleteUserSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "user_delete",
		Group:       skills.GroupAccounts,
		Description: "Delete a user through one configured account provider.",
		Aliases:     []string{"user delete", "delete user", "remove user", "unregister user"},
		Usage:       "/user_delete [provider] <username>",
		Examples: []string{
			"user_delete jitsi anya",
			"user delete anya",
		},
		Mutating: true,
	}
}

func (s *deleteUserSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	provider := strings.TrimSpace(input.Args["provider"])
	username := strings.TrimSpace(input.Args["username"])

	if err := s.manager.DeleteUser(ctx, provider, username); err != nil {
		return skills.Result{}, err
	}

	if provider == "" {
		return skills.Result{Text: "User deleted: " + username}, nil
	}
	return skills.Result{Text: fmt.Sprintf("User deleted: %s (%s)", username, provider)}, nil
}
