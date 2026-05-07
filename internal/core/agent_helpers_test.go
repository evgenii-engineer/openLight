package core

import "testing"

func TestIsBareSlashCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"/vision_analyze":         true,
		"/vision_analyze ":        true,
		"  /vision_analyze  ":     true,
		"/vision_analyze ./img.png": false,
		"/imagegen_create cute":   false,
		"vision_analyze":          false, // no leading slash → not a slash command
		"/":                       true,  // edge: slash with no name; harmless either way
		"":                        false,
		"   ":                     false,
		"/skill\twith tab":        false, // any whitespace separates fields
	}
	for input, want := range cases {
		if got := isBareSlashCommand(input); got != want {
			t.Fatalf("isBareSlashCommand(%q) = %v, want %v", input, got, want)
		}
	}
}
