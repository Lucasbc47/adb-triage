// adb-triage is a TUI for reviewing and uninstalling Android apps over
// adb. It lists user-installed packages grouped by category, with disk usage,
// and lets you multi-select and remove them in one pass.
//
// Friendly names come from a curated table compiled into the binary, falling
// back to a guess derived from the package name. Nothing is fetched and nothing
// is stored, so the tool works the same offline as online.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Lucasbc47/adb-triage/internal/adb"
	"github.com/Lucasbc47/adb-triage/internal/classify"
	"github.com/Lucasbc47/adb-triage/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// readCtx bounds one startup adb read. Each call gets its own deadline rather
// than sharing one budget, so a slow dumpsys does not starve the reads after it.
func readCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), adb.ReadTimeout)
}

func run() error {
	all := flag.Bool("all", false, "include apps with no launcher icon (background services)")
	dump := flag.Bool("dump", false, "print the list as plain text and exit, without opening the TUI")
	asJSON := flag.Bool("json", false, "print the list as JSON and exit, without opening the TUI")
	flag.Parse()

	devCtx, cancelDev := readCtx()
	defer cancelDev()
	devices, err := adb.Devices(devCtx)
	if err != nil {
		return fmt.Errorf("could not talk to adb (is it on your PATH?): %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("no authorized device found: connect your phone and accept the USB debugging prompt")
	}
	if len(devices) > 1 {
		return fmt.Errorf("%d devices connected; disconnect the others (picking one is not supported yet)", len(devices))
	}
	device := devices[0]

	// Progress goes to stderr so `--json > apps.json` stays valid JSON.
	fmt.Fprintf(os.Stderr, "reading packages from %s...\n", device.Model)
	pkgCtx, cancelPkg := readCtx()
	defer cancelPkg()
	pkgs, err := adb.ThirdParty(pkgCtx)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no third-party apps found")
	}

	// Sizes and launchability are nice-to-have; a failure here shouldn't stop
	// the whole tool, so we degrade instead of returning.
	sizeCtx, cancelSize := readCtx()
	defer cancelSize()
	sizes, err := adb.Sizes(sizeCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read app sizes: %v\n", err)
		sizes = map[string]int64{}
	}
	launchCtx, cancelLaunch := readCtx()
	defer cancelLaunch()
	launchable, err := adb.Launchable(launchCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read the launcher list: %v\n", err)
		launchable = map[string]bool{}
	}

	entries := classify.Lookup(pkgs)

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
		return fmt.Errorf("nothing to show (try --all)")
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

	prog := tea.NewProgram(ui.New(device, apps), tea.WithAltScreen())
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
