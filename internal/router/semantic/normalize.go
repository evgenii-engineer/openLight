package semantic

import (
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}\s._/-]+`)

type rewriteRule struct {
	from []string
	to   string
}

var tokenRewriteRules = []rewriteRule{
	{from: []string{"оперативная", "память"}, to: "memory"},
	{from: []string{"оперативную", "память"}, to: "memory"},
	{from: []string{"оперативной", "памяти"}, to: "memory"},
	{from: []string{"покажи"}, to: "show"},
	{from: []string{"посмотри"}, to: "show"},
	{from: []string{"проверь"}, to: "check"},
	{from: []string{"глянь"}, to: "check"},
	{from: []string{"память"}, to: "memory"},
	{from: []string{"оперативка"}, to: "memory"},
	{from: []string{"оперативку"}, to: "memory"},
	{from: []string{"оперативке"}, to: "memory"},
	{from: []string{"оперативной"}, to: "memory"},
	{from: []string{"мемори"}, to: "memory"},
	{from: []string{"рам"}, to: "memory"},
	{from: []string{"ram"}, to: "memory"},
	{from: []string{"цпу"}, to: "cpu"},
	{from: []string{"проц"}, to: "cpu"},
	{from: []string{"процессора"}, to: "cpu"},
	{from: []string{"процессору"}, to: "cpu"},
	{from: []string{"процессор"}, to: "cpu"},
	{from: []string{"загрузка"}, to: "usage"},
	{from: []string{"диск"}, to: "disk"},
	{from: []string{"storage"}, to: "disk"},
	{from: []string{"место"}, to: "disk"},
	{from: []string{"логи"}, to: "logs"},
	{from: []string{"лог"}, to: "logs"},
	{from: []string{"журнал"}, to: "logs"},
	{from: []string{"перезапустить"}, to: "restart"},
	{from: []string{"перезапусти"}, to: "restart"},
	{from: []string{"рестартни"}, to: "restart"},
	{from: []string{"рестарт"}, to: "restart"},
	{from: []string{"сервиса"}, to: "service"},
	{from: []string{"сервисы"}, to: "services"},
	{from: []string{"сервис"}, to: "service"},
	{from: []string{"статус"}, to: "status"},
	{from: []string{"состояние"}, to: "status"},
	{from: []string{"температура"}, to: "temperature"},
	{from: []string{"темпа"}, to: "temperature"},
	{from: []string{"аптайм"}, to: "uptime"},
	{from: []string{"хост"}, to: "hostname"},
	{from: []string{"айпи"}, to: "ip"},
	{from: []string{"заметки"}, to: "notes"},
	{from: []string{"заметку"}, to: "note"},
	{from: []string{"заметка"}, to: "note"},
	{from: []string{"добавить"}, to: "add"},
	{from: []string{"добавь"}, to: "add"},
	{from: []string{"запомни"}, to: "remember"},
	{from: []string{"удалить"}, to: "delete"},
	{from: []string{"удали"}, to: "delete"},
	{from: []string{"скиллах"}, to: "skills"},
	{from: []string{"скиллы"}, to: "skills"},
	{from: []string{"скилы"}, to: "skills"},
	{from: []string{"умеешь"}, to: "skills"},
	{from: []string{"возможности"}, to: "skills"},
	{from: []string{"навыки"}, to: "skills"},
	{from: []string{"интернет"}, to: "network"},
}

func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	value = nonAlnum.ReplaceAllString(value, " ")
	tokens := strings.Fields(value)
	if len(tokens) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(tokens))
	for idx := 0; idx < len(tokens); {
		matched := false
		for _, rule := range tokenRewriteRules {
			if len(rule.from) > len(tokens)-idx {
				continue
			}
			if matchesRule(tokens[idx:], rule.from) {
				normalized = append(normalized, rule.to)
				idx += len(rule.from)
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		normalized = append(normalized, tokens[idx])
		idx++
	}

	return strings.Join(normalized, " ")
}

func Tokens(value string) []string {
	normalized := Normalize(value)
	if normalized == "" {
		return nil
	}
	return strings.Fields(normalized)
}

func matchesRule(tokens []string, pattern []string) bool {
	for idx, token := range pattern {
		if tokens[idx] != token {
			return false
		}
	}
	return true
}
