package callback

import (
	"errors"
	"strconv"
	"strings"
)

// Action is the decoded form of a Telegram callback_data string.
//
// Telegram limits callback_data to 64 bytes, so we keep the grammar
// compact: "<kind>[:<target>[:<extra>]]". The semantics of Target/Extra
// depend on Kind:
//
//	home               -> top-level menu
//	g:<group>          -> open a skill group menu (target=group key)
//	s:<skill>          -> execute a skill (target=skill name)
//	s:<skill>:<argref> -> execute a skill with stored args (extra=arg ref token)
//	a:<action>:<arg>   -> contextual action (target=action name, extra=arg)
//	q:<id>             -> run a configured quick action by id
//	b:<dest>           -> back navigation (e.g. "groups", "g:system")
//	p:<screen>:<page>  -> pagination (extra=page number)
//	c:<token>          -> confirm a pending mutating call
//	x:<token>          -> cancel a pending mutating call
type Action struct {
	Kind   string
	Target string
	Extra  string
}

const (
	KindHome    = "home"
	KindGroup   = "g"
	KindSkill   = "s"
	KindAction  = "a"
	KindQuick   = "q"
	KindBack    = "b"
	KindPage    = "p"
	KindConfirm = "c"
	KindCancel  = "x"
)

var ErrInvalid = errors.New("invalid callback")

// MaxBytes is Telegram's limit for callback_data.
const MaxBytes = 64

func Decode(data string) (Action, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return Action{}, ErrInvalid
	}
	parts := strings.SplitN(data, ":", 3)
	a := Action{Kind: parts[0]}
	if len(parts) > 1 {
		a.Target = parts[1]
	}
	if len(parts) > 2 {
		a.Extra = parts[2]
	}
	return a, nil
}

func Encode(a Action) string {
	out := a.Kind
	if a.Target != "" {
		out += ":" + a.Target
	}
	if a.Extra != "" {
		out += ":" + a.Extra
	}
	return out
}

func Home() string             { return Encode(Action{Kind: KindHome}) }
func Group(key string) string  { return Encode(Action{Kind: KindGroup, Target: key}) }
func Skill(name string) string { return Encode(Action{Kind: KindSkill, Target: name}) }
func SkillWithRef(name, ref string) string {
	return Encode(Action{Kind: KindSkill, Target: name, Extra: ref})
}
func ActionFor(name, target string) string {
	return Encode(Action{Kind: KindAction, Target: name, Extra: target})
}
func Quick(id string) string { return Encode(Action{Kind: KindQuick, Target: id}) }

// Back targets a top-level destination by name. Use BackToGroup for the
// "return to a particular group" case so the encoding stays unambiguous.
func Back(dest string) string { return Encode(Action{Kind: KindBack, Target: dest}) }

// BackToGroup encodes a "return to group <key>" callback.
func BackToGroup(key string) string {
	return Encode(Action{Kind: KindBack, Target: "g", Extra: key})
}

// Page paginates a group menu by group key.
func Page(groupKey string, page int) string {
	return Encode(Action{Kind: KindPage, Target: groupKey, Extra: strconv.Itoa(page)})
}

func Confirm(token string) string { return Encode(Action{Kind: KindConfirm, Target: token}) }
func Cancel(token string) string  { return Encode(Action{Kind: KindCancel, Target: token}) }

// PageNumber decodes the page index from a pagination action's Extra.
func (a Action) PageNumber() int {
	n, err := strconv.Atoi(strings.TrimSpace(a.Extra))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
