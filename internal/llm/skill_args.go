package llm

import "strings"

func allowedArgumentKeysForSkills(allowedSkills []string) []string {
	seen := make(map[string]struct{}, len(allowedSkills))
	result := make([]string, 0, len(allowedSkills))
	for _, skillName := range allowedSkills {
		for _, key := range argumentKeysForSkill(skillName) {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}
	return result
}

func argumentKeysForSkill(skillName string) []string {
	switch strings.TrimSpace(skillName) {
	case "help", "skills":
		return []string{"topic"}
	case "service_status", "service_restart", "service_logs":
		return []string{"service"}
	case "note_add", "chat":
		return []string{"text"}
	case "note_delete", "watch_pause", "watch_remove", "watch_test", "watch_history":
		return []string{"id"}
	case "file_list", "file_read", "exec_file":
		return []string{"path"}
	case "file_write":
		return []string{"path", "content"}
	case "file_replace":
		return []string{"path", "find", "replace"}
	case "exec_code":
		return []string{"runtime", "code"}
	case "watch_add":
		return []string{"spec"}
	case "user_add":
		return []string{"provider", "username", "password"}
	case "user_list":
		return []string{"provider", "pattern"}
	case "user_delete":
		return []string{"provider", "username"}
	default:
		return nil
	}
}

func shortSkillGuide(skillName string) string {
	switch strings.TrimSpace(skillName) {
	case "start":
		return "welcome/onboarding"
	case "ping":
		return "alive check"
	case "skills":
		return "list skill groups"
	case "help":
		return "general help"
	case "status":
		return "overall host status"
	case "cpu":
		return "cpu usage"
	case "memory":
		return "ram usage"
	case "disk":
		return "disk usage"
	case "uptime":
		return "uptime"
	case "hostname":
		return "hostname"
	case "ip":
		return "ip addresses"
	case "temperature":
		return "device temperature"
	case "service_list":
		return "list services"
	case "service_status":
		return "service status"
	case "service_logs":
		return "service logs"
	case "service_restart":
		return "restart service"
	case "note_add":
		return "save note"
	case "note_list":
		return "list notes"
	case "note_delete":
		return "delete note"
	case "watch_add":
		return "create watch"
	case "watch_list":
		return "list watches"
	case "watch_test":
		return "probe watch"
	case "watch_history":
		return "watch incidents"
	case "watch_pause":
		return "pause watch"
	case "watch_remove":
		return "remove watch"
	case "file_list":
		return "list files"
	case "file_read":
		return "read file"
	case "file_write":
		return "write file"
	case "file_replace":
		return "replace text in file"
	case "exec_code":
		return "run code snippet"
	case "exec_file":
		return "run allowed file"
	case "workspace_clean":
		return "clean workspace"
	case "user_providers":
		return "list account providers"
	case "user_add":
		return "add user"
	case "user_list":
		return "list users"
	case "user_delete":
		return "delete user"
	default:
		return "best matching skill"
	}
}
