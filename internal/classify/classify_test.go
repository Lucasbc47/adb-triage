package classify

import (
	"strings"
	"testing"
)

// maxCategoryWidth is the sidebar column's usable width in ui.appRow. classify
// cannot import ui (ui imports classify), so the constraint is asserted here
// against a literal. A category longer than this renders truncated, as
// "Security & accou…".
const maxCategoryWidth = 17

// TestSeedCategoriesAreValid is the guard against the failure mode where a
// label gets pasted into the category slot: the entry then renders as its own
// orphan one-app category, which looks like a UI bug rather than bad data.
func TestSeedCategoriesAreValid(t *testing.T) {
	valid := make(map[string]bool, len(Categories))
	for _, c := range Categories {
		valid[c] = true
	}
	for pkg, e := range seed {
		if !valid[e.Category] {
			t.Errorf("%s: category %q is not in Categories", pkg, e.Category)
		}
	}
}

func TestSeedLabelsAreUsable(t *testing.T) {
	for pkg, e := range seed {
		switch {
		case e.Label == "":
			t.Errorf("%s: empty label", pkg)
		case strings.TrimSpace(e.Label) != e.Label:
			t.Errorf("%s: label %q has surrounding whitespace", pkg, e.Label)
		}
	}
}

// Heuristic falls back to these, so they must satisfy the same rule the seed
// does, or a guessed app lands in a category the sidebar never lists.
func TestHeuristicCategoriesAreValid(t *testing.T) {
	valid := make(map[string]bool, len(Categories))
	for _, c := range Categories {
		valid[c] = true
	}
	for _, pkg := range []string{
		"com.android.settings", "br.gov.dataprev.x", "com.mybank.app",
		"com.foo.game", "com.foo.emu", "com.foo.camera", "com.foo.browser",
		"com.foo.messenger", "com.totally.unknown",
	} {
		if got := Heuristic(pkg).Category; !valid[got] {
			t.Errorf("Heuristic(%q).Category = %q, not in Categories", pkg, got)
		}
	}
}

func TestCategoriesFitSidebar(t *testing.T) {
	for _, c := range Categories {
		if len([]rune(c)) > maxCategoryWidth {
			t.Errorf("category %q is %d runes, over the %d the sidebar shows",
				c, len([]rune(c)), maxCategoryWidth)
		}
	}
}

func TestCategoriesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Categories {
		if seen[c] {
			t.Errorf("duplicate category %q", c)
		}
		seen[c] = true
	}
}

func TestHeuristic(t *testing.T) {
	tests := []struct {
		pkg          string
		wantLabel    string
		wantCategory string
	}{
		{"com.android.settings", "Settings", "System"},
		{"br.gov.meugov.mobile", "Mobile", "Government & ID"},
		{"com.example.mybank", "Mybank", "Banking & finance"},
		{"com.example.my_app", "My App", "Other"},
		{"com.example.some-tool", "Some Tool", "Other"},
		{"singleword", "Singleword", "Other"},
	}
	for _, tt := range tests {
		got := Heuristic(tt.pkg)
		if got.Label != tt.wantLabel {
			t.Errorf("Heuristic(%q).Label = %q, want %q", tt.pkg, got.Label, tt.wantLabel)
		}
		if got.Category != tt.wantCategory {
			t.Errorf("Heuristic(%q).Category = %q, want %q", tt.pkg, got.Category, tt.wantCategory)
		}
	}
}

// Heuristic must never return an empty label: main.go uses it as the last
// resort, so an empty result would render as a blank row. The empty package
// name is excluded deliberately, since adb.ThirdParty drops those and there is
// no sensible label to invent for one.
func TestHeuristicNeverEmpty(t *testing.T) {
	for _, pkg := range []string{".", "...", "com.", "a", "com.example."} {
		if got := Heuristic(pkg).Label; got == "" {
			t.Errorf("Heuristic(%q) produced an empty label", pkg)
		}
	}
}

func TestStripFences(t *testing.T) {
	tests := []struct{ in, want string }{
		{"{\"a\":1}", "{\"a\":1}"},
		{"```json\n{\"a\":1}\n```", "{\"a\":1}"},
		{"```\n{\"a\":1}\n```", "{\"a\":1}"},
	}
	for _, tt := range tests {
		if got := stripFences(tt.in); got != tt.want {
			t.Errorf("stripFences(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
