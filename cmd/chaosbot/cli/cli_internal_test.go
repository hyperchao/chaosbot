package cli

import "testing"

func TestReplComplete_SlashPrefix(t *testing.T) {
	c := replComplete("/")
	if len(c) != 5 {
		t.Fatalf("got %d completions, want 5 (%q)", len(c), c)
	}
	want := map[string]bool{
		"/reset": true, "/exit": true, "/quit": true, "/help": true, "/tools": true,
	}
	for _, s := range c {
		if !want[s] {
			t.Errorf("unexpected completion %q", s)
		}
	}
}

func TestReplComplete_SlashExact(t *testing.T) {
	c := replComplete("/exit")
	if len(c) != 1 || c[0] != "/exit" {
		t.Errorf("got %q, want [\"/exit\"]", c)
	}
}

func TestReplComplete_PartialSlash(t *testing.T) {
	c := replComplete("/h")
	if len(c) != 1 || c[0] != "/help" {
		t.Errorf("got %q, want [\"/help\"]", c)
	}
}

func TestReplComplete_NoPrefixMatch(t *testing.T) {
	c := replComplete("hello")
	if len(c) != 0 {
		t.Errorf("got %d completions, want 0", len(c))
	}
}

func TestReplComplete_EmptyInput(t *testing.T) {
	c := replComplete("")
	if len(c) != 0 {
		t.Errorf("got %d completions, want 0", len(c))
	}
}
