// Package classify turns raw package names into friendly labels and categories.
//
// Resolution happens in three layers, cheapest first:
//
//  1. seed.json, compiled into the binary. Hand-curated, works offline, and is
//     the reason the tool shows "Threads" instead of "com.instagram.barcelona"
//     with no API key and no network.
//  2. The on-disk cache of previous LLM answers.
//  3. Claude, asked only about packages neither layer knows.
//
// A package name is a stable identifier, so a cached or seeded entry never goes
// stale.
package classify

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

//go:embed seed.json
var seedJSON []byte

// Entry is what we know about one package beyond its name.
type Entry struct {
	Label    string `json:"label"`
	Category string `json:"category"`
}

// Categories the model is allowed to choose from. Keeping this list closed
// stops the model from inventing a new bucket for every third app.
var Categories = []string{
	"IA e assistentes",
	"Bancos e financas",
	"Governo e documentos",
	"Transporte",
	"Compras",
	"Delivery",
	"Redes sociais",
	"Mensagens",
	"Streaming e midia",
	"Jogos e emuladores",
	"Dev e ferramentas",
	"Produtividade",
	"Saude e fitness",
	"Navegadores",
	"Seguranca e contas",
	"Fotos e camera",
	"Casa inteligente",
	"Sistema",
	"Outros",
}

const systemPrompt = `You identify Android apps from their package names.

For each package name given, return the app's real display name and one category.

Rules:
- Use the app's actual consumer-facing name ("WhatsApp", "Nubank", "iFood"), not a
  description. If you don't recognize the package, derive a plausible name from the
  package name itself rather than leaving it blank.
- Pick exactly one category from the allowed list. Never invent a category.
- Brazilian apps are common in this data; prefer their Brazilian names.`

var seed = func() map[string]Entry {
	m := map[string]Entry{}
	// A malformed seed is a build-time mistake, not a runtime condition the
	// user can fix, so fail loudly rather than silently shipping no labels.
	if err := json.Unmarshal(seedJSON, &m); err != nil {
		panic("seed.json invalido: " + err.Error())
	}
	return m
}()

// SeedSize reports how many packages ship known in the binary. Useful for the
// help overlay, so it's obvious the offline path has real data behind it.
func SeedSize() int { return len(seed) }

// Lookup resolves what it can without touching the network: the embedded seed
// first, then the on-disk cache, then name heuristics. Every requested package
// gets an entry, so callers never have to handle a missing key.
func Lookup(pkgs []string) map[string]Entry {
	cache, _ := loadCache()
	out := make(map[string]Entry, len(pkgs))
	for _, p := range pkgs {
		switch {
		case seed[p].Label != "":
			out[p] = seed[p]
		case cache[p].Label != "":
			out[p] = cache[p]
		default:
			out[p] = Heuristic(p)
		}
	}
	return out
}

// Known reports whether a package was resolved from real data (seed or cache)
// rather than guessed from its name.
func Known(pkg string) bool {
	if seed[pkg].Label != "" {
		return true
	}
	cache, _ := loadCache()
	return cache[pkg].Label != ""
}

// Classify resolves labels for pkgs, asking Claude only about the ones neither
// the seed nor the cache covers. If the API is unreachable or unauthenticated
// it degrades to Lookup, so the tool still works offline.
func Classify(ctx context.Context, pkgs []string) (map[string]Entry, error) {
	cache, cachePath := loadCache()

	out := make(map[string]Entry, len(pkgs))
	var missing []string
	for _, p := range pkgs {
		switch {
		case seed[p].Label != "":
			out[p] = seed[p]
		case cache[p].Label != "":
			out[p] = cache[p]
		default:
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}

	resolved, err := askClaude(ctx, missing)
	if err != nil {
		// Offline or no credentials. Fill the gaps heuristically and keep
		// going; we deliberately do NOT cache guesses, so a later run with
		// working credentials can still upgrade them.
		for _, p := range missing {
			out[p] = Heuristic(p)
		}
		return out, fmt.Errorf("%d apps sem nome real (LLM indisponivel): %w", len(missing), err)
	}

	for p, e := range resolved {
		if e.Label == "" {
			continue
		}
		cache[p] = e
		out[p] = e
	}
	// A failed cache write costs us nothing but a repeated API call next run.
	if err := saveCache(cachePath, cache); err != nil {
		fmt.Fprintf(os.Stderr, "aviso: nao consegui gravar o cache: %v\n", err)
	}
	for _, p := range missing {
		if out[p].Label == "" {
			out[p] = Heuristic(p) // model skipped one; don't leave a hole
		}
	}
	return out, nil
}

func askClaude(ctx context.Context, pkgs []string) (map[string]Entry, error) {
	client := anthropic.NewClient()

	var b strings.Builder
	b.WriteString("Allowed categories:\n")
	for _, c := range Categories {
		b.WriteString("- " + c + "\n")
	}
	b.WriteString("\nPackages:\n")
	for _, p := range pkgs {
		b.WriteString(p + "\n")
	}
	b.WriteString("\nReturn ONLY a JSON object mapping each package name to " +
		`{"label": ..., "category": ...}. No prose, no markdown fences.`)

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-opus-5",
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(b.String())),
		},
	})
	if err != nil {
		return nil, err
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}

	raw := stripFences(strings.TrimSpace(text.String()))
	if raw == "" {
		return nil, fmt.Errorf("resposta vazia do modelo")
	}

	var out map[string]Entry
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("resposta do modelo nao era JSON valido: %w", err)
	}
	return out, nil
}

// stripFences drops a ```json ... ``` wrapper if the model added one despite
// being told not to.
func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "```")
}

// Heuristic is the last-resort guess for a package the seed, the cache, and the
// model all missed.
func Heuristic(pkg string) Entry {
	lower := strings.ToLower(pkg)
	rules := []struct{ match, category string }{
		{"com.android.", "Sistema"},
		{"com.google.android.", "Sistema"},
		{"com.motorola.", "Sistema"},
		{"com.qualcomm.", "Sistema"},
		{"br.gov.", "Governo e documentos"},
		{"bank", "Bancos e financas"},
		{"game", "Jogos e emuladores"},
		{"emu", "Jogos e emuladores"},
		{"camera", "Fotos e camera"},
		{"browser", "Navegadores"},
		{"messenger", "Mensagens"},
	}
	for _, r := range rules {
		if strings.Contains(lower, r.match) {
			return Entry{Label: prettify(pkg), Category: r.category}
		}
	}
	return Entry{Label: prettify(pkg), Category: "Outros"}
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

func cacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "adb-triage-cache.json"
	}
	return filepath.Join(dir, "adb-triage", "labels.json")
}

func loadCache() (map[string]Entry, string) {
	path := cacheFile()
	cache := map[string]Entry{}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache, path
	}
	// A corrupt cache is not worth failing over; start fresh.
	_ = json.Unmarshal(data, &cache)
	return cache, path
}

func saveCache(path string, cache map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
