package core

import "testing"

func TestIsBareSlashCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"/vision_analyze":           true,
		"/vision_analyze ":          true,
		"  /vision_analyze  ":       true,
		"/vision_analyze ./img.png": false,
		"/imagegen_create cute":     false,
		"vision_analyze":            false, // no leading slash → not a slash command
		"/":                         true,  // edge: slash with no name; harmless either way
		"":                          false,
		"   ":                       false,
		"/skill\twith tab":          false, // any whitespace separates fields
	}
	for input, want := range cases {
		if got := isBareSlashCommand(input); got != want {
			t.Fatalf("isBareSlashCommand(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestCommandToken(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/menu":                   "menu",
		"/Menu":                   "menu",
		"  /vendor_report  ":      "vendor_report",
		"/vendor_report @nick, x": "vendor_report",
		"/note@openlight_bot buy": "note",
		"/menu@openlight_bot":     "menu",
		"/home/damk/shot.png":     "", // a path, not a command
		"/tmp/out.png":            "",
		"menu":                    "", // no leading slash
		"mistake with ad":         "",
		"/":                       "",
		"/@openlight_bot":         "",
		"":                        "",
	}
	for input, want := range cases {
		if got := commandToken(input); got != want {
			t.Fatalf("commandToken(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsMenuCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"/menu":               true,
		"menu":                true,
		"/home":               true,
		"home":                true,
		"  /MENU  ":           true,
		"/menu@openlight_bot": true, // groups glue the bot name on
		"/home@openlight_bot": true,
		"/home/damk/shot.png": false, // a path that merely starts like /home
		"/menu please":        false,
		"/note":               false,
		"":                    false,
	}
	for input, want := range cases {
		if got := isMenuCommand(input); got != want {
			t.Fatalf("isMenuCommand(%q) = %v, want %v", input, got, want)
		}
	}
}
