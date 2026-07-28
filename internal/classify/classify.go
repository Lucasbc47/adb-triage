// Package classify turns raw package names into friendly labels and categories.
//
// Resolution has two layers:
//
//  1. seed.json, compiled into the binary. Hand-curated, and the reason the
//     tool shows "Threads" instead of "com.instagram.barcelona".
//  2. A heuristic derived from the package name itself, for anything the seed
//     does not cover.
//
// There is no network call and no on-disk state: every answer comes from the
// binary or from the package name. A package that resolves to layer 2 is shown
// in italic by the UI, and the way to fix it is a pull request against
// seed.json.
package classify

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed seed.json
var seedJSON []byte

// Entry is what we know about one package beyond its name.
type Entry struct {
	Label    string `json:"label"`
	Category string `json:"category"`
}

// Categories is the closed set a package may be filed under. Keeping it closed
// is what stops the sidebar from growing a new one-app bucket for every third
// package.
//
// Names are kept at or under 17 characters so they fit the sidebar column
// without truncation (see ui.sidebarWidth).
var Categories = []string{
	"AI & assistants",
	"Banking & finance",
	"Government & ID",
	"Transport",
	"Shopping",
	"Food delivery",
	"Social",
	"Messaging",
	"Streaming & media",
	"Games & emulators",
	"Dev & tools",
	"Productivity",
	"Health & fitness",
	"Browsers",
	"Security & auth",
	"Photos & camera",
	"Smart home",
	"System",
	"Other",
}

var seed = func() map[string]Entry {
	m := map[string]Entry{}
	// A malformed seed is a build-time mistake, not a runtime condition the
	// user can fix, so fail loudly rather than silently shipping no labels.
	if err := json.Unmarshal(seedJSON, &m); err != nil {
		panic("invalid seed.json: " + err.Error())
	}
	return m
}()

// SeedSize reports how many packages ship known in the binary. Useful for the
// help overlay, so it's obvious how much the curated table actually covers.
func SeedSize() int { return len(seed) }

// Lookup resolves every requested package, falling back to a name heuristic for
// anything the seed does not know. Every requested package gets an entry, so
// callers never have to handle a missing key.
func Lookup(pkgs []string) map[string]Entry {
	out := make(map[string]Entry, len(pkgs))
	for _, p := range pkgs {
		if seed[p].Label != "" {
			out[p] = seed[p]
			continue
		}
		out[p] = Heuristic(p)
	}
	return out
}

// KnownSet reports, per package, whether it came from the curated seed rather
// than being guessed from its name. The UI uses this to render guesses in
// italic.
func KnownSet(pkgs []string) map[string]bool {
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[p] = seed[p].Label != ""
	}
	return known
}

// Heuristic is the last-resort guess for a package the seed does not cover.
func Heuristic(pkg string) Entry {
	lower := strings.ToLower(pkg)
	rules := []struct{ match, category string }{
		{"com.android.", "System"},
		{"com.google.android.", "System"},
		{"com.motorola.", "System"},
		{"com.qualcomm.", "System"},
		{"br.gov.", "Government & ID"},
		{"bank", "Banking & finance"},
		{"game", "Games & emulators"},
		{"emu", "Games & emulators"},
		{"camera", "Photos & camera"},
		{"browser", "Browsers"},
		{"messenger", "Messaging"},
	}
	for _, r := range rules {
		if strings.Contains(lower, r.match) {
			return Entry{Label: prettify(pkg), Category: r.category}
		}
	}
	return Entry{Label: prettify(pkg), Category: "Other"}
}

// prettify turns "com.example.my_app" into "My App" as a last-resort label.
func prettify(pkg string) string {
	parts := strings.Split(pkg, ".")
	name := parts[len(parts)-1]
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	words := strings.Fields(name)
	for i, w := range words {
		r := []rune(w)
		words[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	if len(words) == 0 {
		return pkg
	}
	return strings.Join(words, " ")
}
