package semantic

import (
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}\s._/-]+`)

var tokenRewrites = strings.NewReplacer(
	"оперативная память", "memory",
	"оперативную память", "memory",
	"покажи", "show",
	"посмотри", "show",
	"проверь", "check",
	"глянь", "check",
	"память", "memory",
	"оперативка", "memory",
	"оперативку", "memory",
	"оперативке", "memory",
	"оперативной", "memory",
	"оперативной памяти", "memory",
	"мемори", "memory",
	"рам", "memory",
	"ram", "memory",
	"цпу", "cpu",
	"проц", "cpu",
	"процессор", "cpu",
	"процессора", "cpu",
	"процессору", "cpu",
	"загрузка", "usage",
	"диск", "disk",
	"storage", "disk",
	"место", "disk",
	"логи", "logs",
	"лог", "logs",
	"журнал", "logs",
	"перезапусти", "restart",
	"перезапустить", "restart",
	"рестартни", "restart",
	"рестарт", "restart",
	"сервис", "service",
	"сервиса", "service",
	"сервисы", "services",
	"статус", "status",
	"состояние", "status",
	"темпа", "temperature",
	"температура", "temperature",
	"аптайм", "uptime",
	"хост", "hostname",
	"айпи", "ip",
	"заметка", "note",
	"заметку", "note",
	"заметки", "notes",
	"добавь", "add",
	"добавить", "add",
	"запомни", "remember",
	"удали", "delete",
	"удалить", "delete",
	"скиллы", "skills",
	"скилы", "skills",
	"скиллах", "skills",
	"умеешь", "skills",
	"возможности", "skills",
	"навыки", "skills",
	"интернет", "network",
)

func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	value = tokenRewrites.Replace(value)
	value = nonAlnum.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func Tokens(value string) []string {
	normalized := Normalize(value)
	if normalized == "" {
		return nil
	}
	return strings.Fields(normalized)
}
