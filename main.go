// adb-triage is a terminal UI for reviewing and uninstalling Android apps over
// adb. It lists user-installed packages grouped by category, with disk usage,
// and lets you multi-select and remove them in one pass.
//
// Friendly names come from a curated table compiled into the binary, then from
// a local cache of previous answers, and only then from Claude. That ordering
// means the common case needs no API key and no network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"adb-triage/internal/adb"
	"adb-triage/internal/classify"
	"adb-triage/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	all := flag.Bool("all", false, "incluir apps sem icone na gaveta (servicos de fundo)")
	useLLM := flag.Bool("llm", false, "consultar o Claude para nomes; sem isso usa so tabela embutida e cache")
	dump := flag.Bool("dump", false, "imprimir a lista em texto e sair, sem abrir o TUI")
	asJSON := flag.Bool("json", false, "imprimir a lista em JSON e sair, sem abrir o TUI")
	flag.Parse()

	devices, err := adb.Devices()
	if err != nil {
		return fmt.Errorf("nao consegui falar com o adb (esta no PATH?): %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("nenhum dispositivo autorizado. Conecte o celular e aceite a depuracao USB")
	}
	if len(devices) > 1 {
		return fmt.Errorf("%d dispositivos conectados; desconecte os outros (ainda nao suporto escolher)", len(devices))
	}
	device := devices[0]

	// Progress goes to stderr so `--json > apps.json` stays valid JSON.
	fmt.Fprintf(os.Stderr, "lendo pacotes de %s...\n", device.Model)
	pkgs, err := adb.ThirdParty()
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("nenhum app de terceiros encontrado")
	}

	// Sizes and launchability are nice-to-have; a failure here shouldn't stop
	// the whole tool, so we degrade instead of returning.
	sizes, err := adb.Sizes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aviso: nao consegui ler os tamanhos: %v\n", err)
		sizes = map[string]int64{}
	}
	launchable, err := adb.Launchable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aviso: nao consegui ler a lista da gaveta: %v\n", err)
		launchable = map[string]bool{}
	}

	var warning string
	var entries map[string]classify.Entry
	if *useLLM {
		fmt.Fprintf(os.Stderr, "resolvendo nomes de %d pacotes...\n", len(pkgs))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		entries, err = classify.Classify(ctx, pkgs)
		if err != nil {
			warning = err.Error()
			fmt.Fprintln(os.Stderr, "aviso:", warning)
		}
	} else {
		entries = classify.Lookup(pkgs)
	}

	apps := make([]*adb.App, 0, len(pkgs))
	for _, p := range pkgs {
		if !*all && len(launchable) > 0 && !launchable[p] {
			continue // background service, not something you'd uninstall by name
		}
		e, ok := entries[p]
		if !ok || e.Label == "" {
			e = classify.Heuristic(p)
		}
		apps = append(apps, &adb.App{
			Pkg:        p,
			Label:      e.Label,
			Category:   e.Category,
			SizeMB:     sizes[p],
			Launchable: launchable[p],
		})
	}
	if len(apps) == 0 {
		return fmt.Errorf("nada pra mostrar (tente --all)")
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Category != apps[j].Category {
			return apps[i].Category < apps[j].Category
		}
		return apps[i].SizeMB > apps[j].SizeMB
	})

	// The non-TUI modes exist so output can be piped, redirected, and copied.
	// Anything printed inside the alt-screen TUI vanishes when it exits.
	if *asJSON {
		return printJSON(device, apps)
	}
	if *dump {
		printDump(device, apps)
		return nil
	}

	prog := tea.NewProgram(ui.New(device, apps, warning), tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

func printJSON(device adb.Device, apps []*adb.App) error {
	type appOut struct {
		Package    string `json:"package"`
		Label      string `json:"label"`
		Category   string `json:"category"`
		SizeMB     int64  `json:"size_mb"`
		Launchable bool   `json:"launchable"`
	}
	out := struct {
		Device string   `json:"device"`
		Serial string   `json:"serial"`
		Apps   []appOut `json:"apps"`
	}{Device: device.Model, Serial: device.Serial}

	for _, a := range apps {
		out.Apps = append(out.Apps, appOut{
			Package: a.Pkg, Label: a.Label, Category: a.Category,
			SizeMB: a.SizeMB, Launchable: a.Launchable,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printDump writes a plain-text table. Deliberately ASCII-only: this output is
// meant to be redirected and pasted, and the Windows console in PowerShell 5.1
// is not UTF-8 by default, so anything fancier arrives as mojibake.
func printDump(device adb.Device, apps []*adb.App) {
	fmt.Printf("%s (%s) - %d apps\n\n", device.Model, device.Serial, len(apps))
	var lastCat string
	var total int64
	for _, a := range apps {
		if a.Category != lastCat {
			fmt.Printf("\n## %s\n", a.Category)
			lastCat = a.Category
		}
		fmt.Printf("  %9s  %-32s %s\n", adb.HumanMB(a.SizeMB), a.Label, a.Pkg)
		total += a.SizeMB
	}
	fmt.Printf("\ntotal: %s\n", adb.HumanMB(total))
}
