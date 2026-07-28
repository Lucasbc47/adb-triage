<h1 align="center">ADB Triage</h1>

<p align="center">
  <img src="./docs/preview.png" alt="adb-triage preview" width="900">
</p>

<p align="center">
  <a href="https://github.com/Lucasbc47/adb-triage/actions/workflows/ci.yml"><img src="https://github.com/Lucasbc47/adb-triage/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="#license"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
  <img src="https://img.shields.io/badge/go-1.24%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.24+">
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey" alt="Platforms">
</p>

A terminal UI for reviewing and uninstalling Android apps over ADB.

Instead of scrolling through dozens of package names like
`com.instagram.barcelona`, **adb-triage** shows each app's real name,
category, and storage usage, making it easy to find and remove what you
actually want.

```text
com.instagram.barcelona     ->  Threads      Social              412 MB
com.nianticlabs.pokemongo   ->  Pokemon GO   Games & emulators   1.8 GB
br.gov.caixa.tem            ->  Caixa Tem    Government & ID      96 MB
```

## Why

`adb shell pm list packages -3` gives you a wall of reverse-DNS strings with no
sizes, no names, and no way to act on them. adb-triage turns that same data into
a browsable, sortable, selectable list, then runs the uninstalls for you in one
pass.

## Features

- Browse installed apps grouped by category, sorted by size
- App display names instead of raw package names
- Per-app storage usage, largest first
- Batch select across categories and uninstall in one confirmation step
- Launch an app on the device to remind yourself what it is before removing it
- Works fully offline using an embedded database of ~160 curated packages
- Local cache so a resolved name is never looked up twice
- Optional Claude integration for packages nothing else recognizes
- `--dump` and `--json` output for scripting and diffing

## Quick start

```sh
adb devices          # confirm the phone shows up as "device"
make build
./adb-triage
```

Move with `↑`/`↓`, switch category with `←`/`→`, mark with `Space`,
uninstall with `d`, quit with `q`.

## Installation

Requirements:

- Go 1.24+
- `adb` on your `PATH` (Android Platform Tools)
- USB debugging enabled, with the device authorized

### Install

```sh
go install github.com/Lucasbc47/adb-triage@latest
```

### From source

```sh
git clone https://github.com/Lucasbc47/adb-triage
cd adb-triage
make build
./adb-triage
```

### Windows

```powershell
winget install Google.PlatformTools
go build .
.\adb-triage.exe
```

### Installing adb

| Platform | Command |
|----------|---------|
| Windows | `winget install Google.PlatformTools` |
| macOS | `brew install --cask android-platform-tools` |
| Debian/Ubuntu | `sudo apt install android-tools-adb` |
| Arch | `sudo pacman -S android-tools` |

## Usage

Enable USB debugging, connect your device, accept the authorization prompt on
the phone, then run:

```sh
./adb-triage
```

By default only apps with a launcher icon are listed, since those are the ones
you would recognize and want to remove. Use `--all` to include background
services and headless packages.

### Flags

| Flag | Description |
|------|-------------|
| `--llm` | Ask Claude about packages the seed and cache do not know. Requires `ANTHROPIC_API_KEY`. |
| `--all` | Include apps without launcher icons (background services). |
| `--dump` | Print the app list as plain text and exit, without opening the TUI. |
| `--json` | Print the app list as JSON and exit, without opening the TUI. |

Progress and warnings go to stderr, so redirecting stdout stays clean:

```sh
./adb-triage --json > apps.json
```

## Keyboard shortcuts

### Browsing

| Key | Action |
|-----|--------|
| `←` `→` `h` `l` `Tab` `Shift+Tab` | Change category (wraps around) |
| `↑` `↓` `j` `k` | Move the cursor |
| `PgUp` `PgDn` | Page through the list |
| `Home` `g` / `End` `G` | Jump to the first or last app |
| `Space` | Select or deselect the current app |
| `a` | Toggle selection for every app in the current category |
| `u` | Clear the whole selection |
| `o` `Enter` | Launch the app on the device |
| `/` | Filter by app name or package |
| `Esc` | Clear the filter |
| `d` | Review and uninstall the selected apps |
| `?` | Show help |
| `q` `Ctrl+C` | Quit |

### Confirmation screen

| Key | Action |
|-----|--------|
| `↑` `↓` `j` `k` | Scroll the list of apps to be removed |
| `y` | Confirm and uninstall |
| any other key | Cancel and go back |

Selections are global, so you can mark apps across multiple categories before
uninstalling. Nothing is removed until you press `d` and then confirm with `y`.

## How labels are resolved

Names and categories are resolved in layers, cheapest first. A package name is a
stable identifier, so a seeded or cached entry never goes stale.

```text
package name
    |
    +-- 1. seed.json          compiled into the binary, offline, no API key
    +-- 2. local cache        previous Claude answers, on disk
    +-- 3. Claude             only with --llm, only for what is still unknown
    +-- 4. heuristic          derived from the package name, shown in italic
```

Anything that falls through to step 4 is displayed in *italic* to make clear the
name is inferred, not known.

### Cache location

Resolved names are written to `adb-triage/labels.json` inside your user cache
directory:

| Platform | Path |
|----------|------|
| Linux | `~/.cache/adb-triage/labels.json` |
| macOS | `~/Library/Caches/adb-triage/labels.json` |
| Windows | `%LOCALAPPDATA%\adb-triage\labels.json` |

Delete the file to force a fresh lookup. Heuristic guesses are deliberately not
cached, so a later run with working credentials can still upgrade them.

### Enabling Claude

```sh
export ANTHROPIC_API_KEY=sk-ant-...
./adb-triage --llm
```

Only package names are sent, never app data, device identifiers, or anything
read off the phone. The lookup covers just the packages the seed and cache
missed, and results are cached, so repeat runs usually make no request at all.
Without `--llm` or an API key the tool works normally, offline.

### Categories

Labels are bucketed into a fixed set, so the list stays stable between runs:

`AI & assistants`, `Banking & finance`, `Government & ID`, `Transport`,
`Shopping`, `Food delivery`, `Social`, `Messaging`, `Streaming & media`,
`Games & emulators`, `Dev & tools`, `Productivity`, `Health & fitness`,
`Browsers`, `Security & auth`, `Photos & camera`, `Smart home`, `System`,
`Other`.

Names are kept to 17 characters or fewer so they fit the sidebar column without
truncating. A test enforces both that limit and that every `seed.json` category
is one of these.

## Scripting

`--json` emits a single object with the device and every app:

```json
{
  "device": "Pixel_7",
  "serial": "1A2B3C4D",
  "apps": [
    {
      "package": "com.instagram.barcelona",
      "label": "Threads",
      "category": "Redes sociais",
      "size_mb": 412,
      "launchable": true
    }
  ]
}
```

Useful one-liners:

```sh
# 10 largest apps
./adb-triage --json | jq -r '.apps | sort_by(-.size_mb)[:10][] | "\(.size_mb)MB\t\(.label)"'

# total space used by games
./adb-triage --json | jq '[.apps[] | select(.category == "Jogos e emuladores") | .size_mb] | add'

# snapshot before and after a cleanup
./adb-triage --dump > before.txt
```

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `nao consegui falar com o adb` | `adb` is not on your `PATH`. Install Platform Tools and reopen the shell. |
| `nenhum dispositivo autorizado` | Enable USB debugging, replug the cable, and accept the prompt on the phone. Verify with `adb devices`. |
| `N dispositivos conectados` | Only one device at a time is supported. Disconnect the others, including emulators. |
| `aviso: nao consegui ler os tamanhos` | `dumpsys diskstats` is restricted on some ROMs. The tool continues without sizes. |
| App list looks short | Background services are hidden by default. Run with `--all`. |
| Uninstall fails | The package is a protected system app that `pm uninstall --user 0` cannot touch. |
| Sizes look too small | `dumpsys diskstats` excludes media under `/sdcard/Android/media`, which dominates for apps like WhatsApp. |
| `context deadline exceeded` | The device stopped responding. Reads time out after 30s and uninstalls after 2min. Unlock the screen, replug, and retry. |

## Contributing

The most useful contribution is seed data. If an app shows up with an *italic*
inferred name, add it to `internal/classify/seed.json` and open a pull request:

```json
"com.example.app": {
  "label": "Example App",
  "category": "Shopping"
}
```

Use the app's real consumer-facing name and one category from the list above.
Brazilian apps keep their Brazilian names (`Nubank`, `iFood`, `Meu Vivo`), since
those are proper nouns rather than translations.

Then rebuild, keep the file formatted consistently, and run the tests, which
check that every category you used actually exists:

```sh
make fmt-seed
make build
go test ./...
```

### Make targets

| Target | What it does |
|--------|--------------|
| `make build` | Build the binary for the current platform |
| `make run` | Build, then run |
| `make test` | Run the test suite |
| `make fmt-seed` | Restore `seed.json`'s aligned, grouped layout |
| `make clean` | Remove built binaries |

## Notes

- Uninstalling an app removes its local data. There is no undo.
- Storage sizes come from `dumpsys diskstats` and may not include media stored
  under `/sdcard/Android/media`.
- Some system applications cannot be removed via `pm uninstall --user 0`.
- Removing a package for user 0 does not delete the APK from a read-only system
  partition, so a factory reset can bring preinstalled apps back.

## Project structure

```text
main.go                  flags, device selection, non-TUI output modes
internal/
├── adb/                 thin wrapper over the adb CLI
├── classify/            label resolution: seed, cache, Claude, heuristic
│   └── seed.json        curated package database, embedded at build time
└── ui/                  Bubble Tea model, views, and key handling
cmd/
└── seedfmt/             formatter for seed.json
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## License

MIT. See [LICENSE](./LICENSE).
