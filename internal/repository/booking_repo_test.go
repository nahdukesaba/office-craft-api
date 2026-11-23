package repository

import "testing"

func TestLikeContainsEscapesWildcards(t *testing.T) {
	cases := map[string]string{
		"budi":       "%budi%",
		"100%":       `%100\%%`,
		"a_b":        `%a\_b%`,
		`back\slash`: `%back\\slash%`,
	}
	for input, want := range cases {
		if got := likeContains(input); got != want {
			t.Errorf("likeContains(%q) = %q, want %q", input, got, want)
		}
	}
}
