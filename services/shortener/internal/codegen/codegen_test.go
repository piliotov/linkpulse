package codegen

import (
	"strings"
	"testing"
)

func TestNewLengthAndAlphabet(t *testing.T) {
	for _, n := range []int{1, 7, 16} {
		code, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != n {
			t.Fatalf("len = %d, want %d", len(code), n)
		}
		for _, c := range code {
			if !strings.ContainsRune(alphabet, c) {
				t.Fatalf("unexpected rune %q in %q", c, code)
			}
		}
	}
}

func TestNewIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		code, _ := New(7)
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
	}
}

func TestValid(t *testing.T) {
	cases := map[string]bool{
		"abc":                   true,
		"my-link_2":             true,
		"ab":                    false,
		"has space":             false,
		"slash/code":            false,
		"":                      false,
		strings.Repeat("a", 32): true,
		strings.Repeat("a", 33): false,
		"api":                   true,
	}
	for in, want := range cases {
		if got := Valid(in); got != want {
			t.Errorf("Valid(%q) = %v, want %v", in, got, want)
		}
	}
}
