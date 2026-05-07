package browser

import (
	"testing"

	"openlight/internal/skills"
)

func TestResolveURLArgPrefersValidArg(t *testing.T) {
	t.Parallel()
	got := resolveURLArg(skills.Input{Args: map[string]string{"url": "example.com"}}, []string{"/browser_screenshot"})
	if got != "example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveURLArgIgnoresNoiseAndScansRawText(t *testing.T) {
	t.Parallel()
	got := resolveURLArg(skills.Input{
		Args:    map[string]string{"url": "сайта"},
		RawText: "пройди по ссылке в браузер исделай скриншот с сайта example.com",
	}, []string{"/browser_screenshot"})
	if got != "example.com" {
		t.Fatalf("expected to recover example.com from raw text, got %q", got)
	}
}

func TestResolveURLArgFallsBackToRawWhenNoArg(t *testing.T) {
	t.Parallel()
	got := resolveURLArg(skills.Input{
		RawText: "/browser_screenshot https://github.com",
	}, []string{"/browser_screenshot"})
	if got != "https://github.com" {
		t.Fatalf("got %q", got)
	}
}

func TestLooksLikeURL(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"example.com":           true,
		"https://example.com":   true,
		"http://localhost:8080": true,
		"localhost":             true,
		"./img.png":             true, // contains slash, treat as path-style URL placeholder
		"github":                false,
		"сайта":                 false,
		"":                      false,
		"  ":                    false,
	}
	for input, want := range cases {
		if got := looksLikeURL(input); got != want {
			t.Fatalf("looksLikeURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestFindFirstURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"скриншот сайта example.com please":   "example.com",
		"go to https://github.com/foo for me": "https://github.com/foo",
		"check news.ycombinator.com":          "news.ycombinator.com",
		"no urls here just words":             "",
		"кириллица.рф shouldn't match":        "",
	}
	for input, want := range cases {
		if got := findFirstURL(input); got != want {
			t.Fatalf("findFirstURL(%q) = %q, want %q", input, got, want)
		}
	}
}
