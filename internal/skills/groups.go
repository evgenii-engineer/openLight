package skills

import "strings"

type Group struct {
	Key         string
	Title       string
	Description string
	Order       int
}

var (
	GroupChat = Group{
		Key:         "chat",
		Title:       "Chat",
		Description: "Free-form conversation with the configured LLM.",
		Order:       0,
	}
	GroupNotes = Group{
		Key:         "notes",
		Title:       "Notes",
		Description: "Save, list, and delete short notes.",
		Order:       1,
	}
	GroupFiles = Group{
		Key:         "files",
		Title:       "Files",
		Description: "Read, list, write, and replace text inside whitelisted paths.",
		Order:       2,
	}
	GroupWorkbench = Group{
		Key:         "workbench",
		Title:       "Workbench",
		Description: "Run temporary code in a workspace or execute explicitly allowed files.",
		Order:       3,
	}
	GroupServices = Group{
		Key:         "services",
		Title:       "Services",
		Description: "Inspect whitelisted services, view logs, and restart them.",
		Order:       4,
	}
	GroupWatch = Group{
		Key:         "watch",
		Title:       "Watch",
		Description: "Monitor allowlisted services and local metrics, then notify, ask, or auto-react.",
		Order:       5,
	}
	GroupAccounts = Group{
		Key:         "accounts",
		Title:       "Accounts",
		Description: "Manage configured application users through explicit account providers.",
		Order:       6,
	}
	GroupSystem = Group{
		Key:         "system",
		Title:       "System",
		Description: "Overall host status plus cpu, memory, disk, uptime, hostname, ip, and temperature.",
		Order:       7,
	}
	GroupCore = Group{
		Key:         "core",
		Title:       "Core",
		Description: "Help, discovery, and core bot commands.",
		Order:       8,
	}
	GroupOther = Group{
		Key:         "other",
		Title:       "Other",
		Description: "Other built-in tools.",
		Order:       99,
	}
)

func normalizeGroup(group Group) Group {
	group.Key = strings.ToLower(strings.TrimSpace(group.Key))
	group.Title = strings.TrimSpace(group.Title)
	group.Description = strings.TrimSpace(group.Description)

	if group.Key == "" {
		return GroupOther
	}
	if group.Title == "" {
		group.Title = group.Key
	}
	if group.Description == "" {
		group.Description = group.Title
	}

	return group
}
