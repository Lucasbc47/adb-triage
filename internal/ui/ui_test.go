package ui

import (
	"testing"

	"github.com/Lucasbc47/adb-triage/internal/adb"
)

func app(pkg string) *adb.App { return &adb.App{Pkg: pkg, Label: pkg} }

// removeUninstalled decides which apps actually left the device. It reads
// results positionally against queue, so a partial result set (the uninstall
// was still running) must leave the un-attempted apps in place.
func TestRemoveUninstalled(t *testing.T) {
	a, b, c := app("com.a"), app("com.b"), app("com.c")
	apps := []*adb.App{a, b, c}

	tests := []struct {
		name    string
		queue   []*adb.App
		results []uninstallStepMsg
		want    []string
	}{
		{
			name:    "all succeed",
			queue:   []*adb.App{a, b},
			results: []uninstallStepMsg{{text: "removed a"}, {text: "removed b"}},
			want:    []string{"com.c"},
		},
		{
			name:    "one fails and stays in the list",
			queue:   []*adb.App{a, b},
			results: []uninstallStepMsg{{text: "failed a", failed: true}, {text: "removed b"}},
			want:    []string{"com.a", "com.c"},
		},
		{
			name:    "fewer results than queued leaves the rest alone",
			queue:   []*adb.App{a, b},
			results: []uninstallStepMsg{{text: "removed a"}},
			want:    []string{"com.b", "com.c"},
		},
		{
			name:    "no results removes nothing",
			queue:   []*adb.App{a, b},
			results: nil,
			want:    []string{"com.a", "com.b", "com.c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeUninstalled(apps, tt.queue, tt.results)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d apps, want %d", len(got), len(tt.want))
			}
			for i, a := range got {
				if a.Pkg != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, a.Pkg, tt.want[i])
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"truncate me please", 8, "truncat…"},
		{"anything", 0, ""},
		{"anything", -1, ""},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestPadTo(t *testing.T) {
	if got := padTo("ab", 5); got != "ab   " {
		t.Errorf("padTo(\"ab\", 5) = %q", got)
	}
	// Already at or over width: never truncates, that is truncate's job.
	if got := padTo("abcdef", 3); got != "abcdef" {
		t.Errorf("padTo over width = %q, want it left alone", got)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{11, 0, 10, 10},
		{5, 0, 0, 0}, // empty range collapses to lo
	}
	for _, tt := range tests {
		if got := clamp(tt.v, tt.lo, tt.hi); got != tt.want {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.want)
		}
	}
}

func TestSortBySize(t *testing.T) {
	in := []*adb.App{
		{Pkg: "small", SizeMB: 10, Label: "b"},
		{Pkg: "big", SizeMB: 100, Label: "a"},
		{Pkg: "tie", SizeMB: 10, Label: "a"},
	}
	got := sortBySize(in)
	want := []string{"big", "tie", "small"} // size desc, then label asc
	for i, w := range want {
		if got[i].Pkg != w {
			t.Errorf("[%d] = %q, want %q", i, got[i].Pkg, w)
		}
	}
	// The input must not be reordered: rebuild groups off m.apps repeatedly.
	if in[0].Pkg != "small" {
		t.Error("sortBySize mutated its input")
	}
}
