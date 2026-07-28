package classify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// KnownSet resolves the whole app list against the on-disk label cache. It is
// called once at startup, per app on the device, so its cost is what the user
// waits through before the TUI paints.
//
// The cache size is the axis that matters: the file grows with every --llm run,
// so this benchmark guards against reintroducing a per-package read, where cost
// would scale with apps x cached entries instead of apps + entries.

// seedCache points os.UserCacheDir at a temp dir holding n cached labels.
// n == 0 leaves the file absent, the state on a machine that never ran --llm.
func seedCache(b *testing.B, n int) {
	dir := b.TempDir()
	b.Setenv("LocalAppData", dir) // what os.UserCacheDir reads on Windows
	b.Setenv("XDG_CACHE_HOME", dir)
	if n == 0 {
		return
	}
	m := make(map[string]Entry, n)
	for i := range n {
		m[fmt.Sprintf("com.cached.app%d", i)] = Entry{
			Label: fmt.Sprintf("Cached App %d", i), Category: "Other",
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "adb-triage", "labels.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatal(err)
	}
}

// unknownPkgs builds a workload of packages absent from the seed, the expensive
// path: a seed hit returns before the cache is ever consulted.
func unknownPkgs(n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = fmt.Sprintf("com.unknown.app%d", i)
	}
	return out
}

func BenchmarkKnownSet(b *testing.B) {
	const apps = 150 // a realistic phone

	for _, cacheSize := range []int{0, 200, 1000} {
		b.Run(fmt.Sprintf("cache=%d", cacheSize), func(b *testing.B) {
			p := unknownPkgs(apps)
			seedCache(b, cacheSize)
			b.ResetTimer()
			for b.Loop() {
				_ = KnownSet(p)
			}
		})
	}
}
