package keyboards

import (
	"strings"

	"openlight/internal/skills"
	"openlight/internal/telegram"
	"openlight/internal/telegram/ui/callback"
)

const (
	// columns is the default keyboard width for inline button grids.
	columns = 2
	// pageSize is the maximum number of skill buttons per group page.
	pageSize = 8
)

// RootReply returns the persistent reply keyboard shown at the root level.
// Tapping a label sends the label as text; the UI maps it to a screen.
func RootReply() [][]string {
	return [][]string{
		{"Skills", "AI"},
		{"Watches", "Services"},
		{"System", "Status"},
	}
}

// RootReplyLabels returns the labels used in RootReply, useful for matching
// inbound messages without duplicating strings.
func RootReplyLabels() []string {
	return []string{"Skills", "AI", "Watches", "Services", "System", "Status"}
}

// GroupsMenu builds an inline keyboard listing all non-empty skill groups.
func GroupsMenu(reg *skills.Registry) [][]telegram.Button {
	groups := reg.ListGroups()
	btns := make([]telegram.Button, 0, len(groups))
	for _, g := range groups {
		if len(visibleDefs(reg, g.Key)) == 0 {
			continue
		}
		btns = append(btns, telegram.Button{
			Text:         g.Title,
			CallbackData: callback.Group(g.Key),
		})
	}
	rows := chunk(btns, columns)
	rows = append(rows, []telegram.Button{
		{Text: "🏠 Home", CallbackData: callback.Home()},
	})
	return rows
}

// GroupMenu builds an inline keyboard listing the skills in a single group,
// paginated when the group has more than pageSize visible skills.
func GroupMenu(reg *skills.Registry, groupKey string, page int) [][]telegram.Button {
	defs := visibleDefs(reg, groupKey)

	if page < 0 {
		page = 0
	}
	start := page * pageSize
	if start >= len(defs) {
		start = 0
		page = 0
	}
	end := start + pageSize
	if end > len(defs) {
		end = len(defs)
	}

	btns := make([]telegram.Button, 0, end-start)
	for _, d := range defs[start:end] {
		btns = append(btns, telegram.Button{
			Text:         titleFor(d),
			CallbackData: callback.Skill(d.Name),
		})
	}
	rows := chunk(btns, columns)

	if len(defs) > pageSize {
		rows = append(rows, paginationRow(groupKey, page, len(defs)))
	}

	rows = append(rows, []telegram.Button{
		{Text: "◀ Back", CallbackData: callback.Back("groups")},
		{Text: "🏠 Home", CallbackData: callback.Home()},
	})
	return rows
}

// SkillFollowUps builds the buttons shown beneath a skill execution result.
// It always includes Refresh + Back; declared FollowUps are appended in order.
func SkillFollowUps(def skills.Definition, hints skills.UIDescriptor) [][]telegram.Button {
	btns := []telegram.Button{
		{Text: "↻ Refresh", CallbackData: callback.Skill(def.Name)},
	}
	for _, fu := range hints.FollowUps {
		label := strings.TrimSpace(fu.Label)
		if label == "" {
			continue
		}
		btns = append(btns, telegram.Button{
			Text:         label,
			CallbackData: callback.ActionFor(fu.Action, fu.Target),
		})
	}
	rows := chunk(btns, columns)
	rows = append(rows, []telegram.Button{
		{Text: "◀ Back", CallbackData: callback.BackToGroup(def.Group.Key)},
		{Text: "🏠 Home", CallbackData: callback.Home()},
	})
	return rows
}

// ConfirmKeyboard builds the Confirm/Cancel pair for a mutating skill.
func ConfirmKeyboard(token string) [][]telegram.Button {
	return [][]telegram.Button{{
		{Text: "✓ Confirm", CallbackData: callback.Confirm(token)},
		{Text: "✗ Cancel", CallbackData: callback.Cancel(token)},
	}}
}

// CancelOnly returns a single-row keyboard with a Cancel button used during
// conversational input flows.
func CancelOnly(backTarget callback.Action) [][]telegram.Button {
	if backTarget.Kind == "" {
		backTarget = callback.Action{Kind: callback.KindBack, Target: "groups"}
	}
	return [][]telegram.Button{{
		{Text: "✗ Cancel", CallbackData: callback.Encode(backTarget)},
	}}
}

// QuickActionsMenu builds the inline keyboard for the Quick Actions screen.
type QuickActionMeta struct {
	ID    string
	Label string
}

func QuickActionsMenu(actions []QuickActionMeta) [][]telegram.Button {
	btns := make([]telegram.Button, 0, len(actions))
	for _, a := range actions {
		btns = append(btns, telegram.Button{
			Text:         a.Label,
			CallbackData: callback.Quick(a.ID),
		})
	}
	rows := chunk(btns, columns)
	rows = append(rows, []telegram.Button{
		{Text: "🏠 Home", CallbackData: callback.Home()},
	})
	return rows
}

func visibleDefs(reg *skills.Registry, groupKey string) []skills.Definition {
	defs := reg.ListByGroup(groupKey)
	out := make([]skills.Definition, 0, len(defs))
	for _, d := range defs {
		if d.Hidden {
			continue
		}
		out = append(out, d)
	}
	return out
}

func paginationRow(groupKey string, page, total int) []telegram.Button {
	row := make([]telegram.Button, 0, 2)
	if page > 0 {
		row = append(row, telegram.Button{
			Text:         "◀ Prev",
			CallbackData: callback.Page(groupKey, page-1),
		})
	}
	if (page+1)*pageSize < total {
		row = append(row, telegram.Button{
			Text:         "Next ▶",
			CallbackData: callback.Page(groupKey, page+1),
		})
	}
	return row
}

func chunk(buttons []telegram.Button, perRow int) [][]telegram.Button {
	if perRow <= 0 {
		perRow = 1
	}
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]telegram.Button, 0, (len(buttons)+perRow-1)/perRow)
	for i := 0; i < len(buttons); i += perRow {
		end := i + perRow
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, append([]telegram.Button(nil), buttons[i:end]...))
	}
	return rows
}

func titleFor(d skills.Definition) string {
	label := humanize(d.Name)
	if d.Mutating {
		return "⚠ " + label
	}
	return label
}

func humanize(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	name = strings.ReplaceAll(name, "_", " ")
	parts := strings.Fields(name)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
