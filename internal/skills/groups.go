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
	GroupServices = Group{
		Key:         "services",
		Title:       "Services",
		Description: "Inspect whitelisted services, view logs, and restart them.",
		Order:       2,
	}
	GroupSystem = Group{
		Key:         "system",
		Title:       "System",
		Description: "Overall host status plus cpu, memory, disk, uptime, hostname, ip, and temperature.",
		Order:       3,
	}
	GroupCore = Group{
		Key:         "core",
		Title:       "Core",
		Description: "Help, discovery, and core bot commands.",
		Order:       4,
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
