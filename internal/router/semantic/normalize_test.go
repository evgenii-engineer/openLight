package semantic

import "testing"

func TestNormalizePreservesWholeWordRewrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{input: "статус сервиса", want: "status service"},
		{input: "загрузка процессора", want: "usage cpu"},
		{input: "оперативной памяти", want: "memory"},
		{input: "покажи логи tailscale", want: "show logs tailscale"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := Normalize(tc.input); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
