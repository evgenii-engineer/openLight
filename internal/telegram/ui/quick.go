package ui

// DefaultQuickActions returns a small built-in catalogue of quick actions that
// map to existing registered skills. Callers may extend or override this list
// at construction time. Each action resolves to a registered skill at runtime,
// so unknown skills surface a clear error instead of executing arbitrary code.
func DefaultQuickActions() []QuickAction {
	return []QuickAction{
		{
			ID:        "system_status",
			Label:     "📊 Status",
			SkillName: "status",
		},
		{
			ID:        "disk",
			Label:     "💽 Disk",
			SkillName: "disk",
		},
		{
			ID:        "cpu",
			Label:     "🧠 CPU",
			SkillName: "cpu",
		},
		{
			ID:        "memory",
			Label:     "💾 Memory",
			SkillName: "memory",
		},
		{
			ID:        "watch_list",
			Label:     "👁 Watches",
			SkillName: "watch_list",
		},
		{
			ID:        "service_list",
			Label:     "🧩 Services",
			SkillName: "service_list",
		},
	}
}
