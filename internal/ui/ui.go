// Package ui implements the Bubble Tea interface for browsing and uninstalling
// apps.
//
// Categories are a column down the left; the list beside it shows only the
// active category's apps. Selection is keyed by package name and therefore
// survives category switches, so you can mark apps across several categories
// and remove them in one pass. Uninstalling always routes through a full-screen
// review of exactly what is about to be removed.
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Lucasbc47/adb-triage/internal/adb"
	"github.com/Lucasbc47/adb-triage/internal/classify"
)

const (
	allTab = "All"
	selTab = "Selected"
)

var (
	appTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0E1413")).Background(lipgloss.Color("#4FB3A6")).Padding(0, 1)
	deviceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8CA09B"))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#24312E"))

	sideActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0E1413")).Background(lipgloss.Color("#4FB3A6"))
	sideMarked = lipgloss.NewStyle().Foreground(lipgloss.Color("#E08268"))
	sideIdle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#B7C4C0"))

	rowCursor  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D9A441"))
	rowActive  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E6EDEB"))
	rowChecked = lipgloss.NewStyle().Foreground(lipgloss.Color("#E08268"))
	guessStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7A76")).Italic(true)
	sizeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7BA9E0"))
	dim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#8CA09B"))

	keyStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D9A441"))
	dangerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E08268"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#58C08D"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#D9A441"))
)

// tab is one row in the category column. cursor and offset are remembered per
// tab so switching away and back lands you where you left off.
type tab struct {
	name   string
	apps   []*adb.App
	sizeMB int64
	cursor int
	offset int
}

type mode int

const (
	modeBrowse mode = iota
	modeFilter
	modeConfirm
	modeHelp
	modeRunning // uninstalling, one package at a time, showing progress
	modeDone
)

// Model is the Bubble Tea model. Construct it with New.
type Model struct {
	device   adb.Device
	apps     []*adb.App
	selected map[string]bool
	// known marks which labels came from the curated seed rather than being
	// guessed. Precomputed once so rendering stays a map lookup per row.
	known map[string]bool

	tabs   []tab
	active int

	height int // full terminal height
	width  int

	mode          mode
	filter        string
	flash         string // result of the last action, cleared on the next key
	results       []uninstallStepMsg
	confirmScroll int

	// Uninstall progress. queue is what's left to remove; the removal runs one
	// package per command so the UI stays responsive between each.
	queue   []*adb.App
	doneN   int
	current string
}

// New builds a model over the given apps.
func New(device adb.Device, apps []*adb.App) Model {
	pkgs := make([]string, len(apps))
	for i, a := range apps {
		pkgs[i] = a.Pkg
	}
	known := classify.KnownSet(pkgs)
	m := Model{
		device:   device,
		apps:     apps,
		selected: map[string]bool{},
		known:    known,
		height:   30,
		width:    100,
	}
	m.rebuild()
	return m
}

// rebuild regenerates the tab set from apps + filter. The active tab and each
// tab's cursor are preserved by name rather than index, since filtering can add
// or drop tabs.
func (m *Model) rebuild() {
	activeName := allTab
	if m.active < len(m.tabs) {
		activeName = m.tabs[m.active].name
	}
	prevCursor := map[string]int{}
	for _, t := range m.tabs {
		prevCursor[t.name] = t.cursor
	}

	groups := map[string][]*adb.App{}
	var all []*adb.App
	for _, a := range m.apps {
		if !m.matches(a) {
			continue
		}
		groups[a.Category] = append(groups[a.Category], a)
		all = append(all, a)
	}

	names := make([]string, 0, len(groups))
	for c := range groups {
		names = append(names, c)
	}
	// Heaviest categories first: that's the order you care about when the goal
	// is reclaiming space.
	sort.Slice(names, func(i, j int) bool {
		si, sj := totalSize(groups[names[i]]), totalSize(groups[names[j]])
		if si != sj {
			return si > sj
		}
		return names[i] < names[j]
	})

	m.tabs = m.tabs[:0]
	m.tabs = append(m.tabs, tab{name: allTab, apps: sortBySize(all), sizeMB: totalSize(all)})

	// The selected view deliberately ignores the filter. It is the review
	// surface for a destructive action, so hiding a pending removal behind an
	// active filter would be exactly the wrong behaviour.
	sel := m.selectedApps()
	m.tabs = append(m.tabs, tab{name: selTab, apps: sel, sizeMB: totalSize(sel)})

	for _, n := range names {
		m.tabs = append(m.tabs, tab{name: n, apps: sortBySize(groups[n]), sizeMB: totalSize(groups[n])})
	}

	m.active = 0
	for i := range m.tabs {
		if m.tabs[i].name == activeName {
			m.active = i
		}
		if c, ok := prevCursor[m.tabs[i].name]; ok {
			m.tabs[i].cursor = clamp(c, 0, max(0, len(m.tabs[i].apps)-1))
		}
	}
	m.clampOffset()
}

func sortBySize(apps []*adb.App) []*adb.App {
	out := make([]*adb.App, len(apps))
	copy(out, apps)
	sort.Slice(out, func(i, j int) bool {
		if out[i].SizeMB != out[j].SizeMB {
			return out[i].SizeMB > out[j].SizeMB
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func totalSize(apps []*adb.App) int64 {
	var t int64
	for _, a := range apps {
		t += a.SizeMB
	}
	return t
}

func (m Model) matches(a *adb.App) bool {
	if m.filter == "" {
		return true
	}
	f := strings.ToLower(m.filter)
	return strings.Contains(strings.ToLower(a.Label), f) ||
		strings.Contains(strings.ToLower(a.Pkg), f)
}

// cur returns the active tab. The tab slice always holds at least allTab, so
// this never returns nil.
func (m *Model) cur() *tab { return &m.tabs[m.active] }

// sidebarWidth is the category column. Wide enough for the longest category
// name plus its counts, narrow enough to leave the list room to breathe.
const sidebarWidth = 26

// listHeight is how many rows the body occupies. The chrome around it is seven
// fixed rows (title, rule, rule, status, flash, and two help rows); the eighth
// keeps the last line clear of the bottom edge, which some terminals scroll.
func (m Model) listHeight() int {
	return max(3, m.height-8)
}

// Init names the terminal window. This goes out as an OSC 2 escape sequence,
// not a shell command, so it behaves the same on every platform.
func (m Model) Init() tea.Cmd { return tea.SetWindowTitle("adb-triage") }

// uninstallStepMsg reports the result of removing one package. Removals run one
// per command, not all in a single blocking batch: adb uninstall can take many
// seconds each on some devices, and a 12-app batch in one command would freeze
// the UI for minutes with no feedback.
//
// failed is a field rather than a prefix on text because removeUninstalled uses
// it to decide which apps actually left the device. Deriving that from display
// text would make a wording change silently corrupt the app list.
type uninstallStepMsg struct {
	text   string
	failed bool
}

// uninstallStep removes exactly one package and reports back.
func uninstallStep(a *adb.App) tea.Cmd {
	return func() tea.Msg {
		// Per-step deadline rather than one for the whole queue: a single wedged
		// package should fail and let the remaining removals proceed.
		ctx, cancel := context.WithTimeout(context.Background(), adb.UninstallTimeout)
		defer cancel()
		if err := adb.Uninstall(ctx, a.Pkg); err != nil {
			return uninstallStepMsg{
				text:   "failed   " + a.Label + " (" + a.Pkg + "): " + err.Error(),
				failed: true,
			}
		}
		return uninstallStepMsg{text: "removed  " + a.Label + " (" + a.Pkg + ")"}
	}
}

// launchDoneMsg reports the outcome of opening an app. Launching is a round
// trip to the device, so it runs as a command rather than blocking the UI.
type launchDoneMsg struct {
	label string
	err   error
}

func launchCmd(pkg, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adb.LaunchTimeout)
		defer cancel()
		return launchDoneMsg{label: label, err: adb.Launch(ctx, pkg)}
	}
}

// startUninstall enters the running state and kicks off the first removal. Each
// uninstallStepMsg advances the queue in Update.
func (m *Model) startUninstall() tea.Cmd {
	m.queue = m.selectedApps()
	m.doneN = 0
	m.results = nil
	m.mode = modeRunning
	if len(m.queue) == 0 {
		m.mode = modeDone
		return nil
	}
	m.current = m.queue[0].Label
	return uninstallStep(m.queue[0])
}

// removeUninstalled drops packages that were successfully removed from apps,
// so returning to browse doesn't keep listing apps that are already gone.
// results is positional against queue: each uninstallStep appends exactly one
// entry before advancing, in queue order.
func removeUninstalled(apps, queue []*adb.App, results []uninstallStepMsg) []*adb.App {
	removed := map[string]bool{}
	for i, a := range queue {
		if i < len(results) && !results[i].failed {
			removed[a.Pkg] = true
		}
	}
	out := make([]*adb.App, 0, len(apps))
	for _, a := range apps {
		if !removed[a.Pkg] {
			out = append(out, a)
		}
	}
	return out
}

// selectedApps returns every marked app, heaviest first. This is the list the
// confirmation screen shows and the order the uninstall runs in.
func (m Model) selectedApps() []*adb.App {
	var out []*adb.App
	for _, a := range m.apps {
		if m.selected[a.Pkg] {
			out = append(out, a)
		}
	}
	return sortBySize(out)
}

func (m Model) selectedPkgs() []string {
	apps := m.selectedApps()
	pkgs := make([]string, len(apps))
	for i, a := range apps {
		pkgs[i] = a.Pkg
	}
	return pkgs
}

func (m Model) selectedSize() int64 { return totalSize(m.selectedApps()) }

// markedIn counts selected apps in one tab, so the tab bar can show where the
// pending removals actually live.
func (m Model) markedIn(t tab) int {
	n := 0
	for _, a := range t.apps {
		if m.selected[a.Pkg] {
			n++
		}
	}
	return n
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(60, msg.Width)
		m.height = max(14, msg.Height)
		m.clampOffset()
		return m, nil

	case uninstallStepMsg:
		m.results = append(m.results, msg)
		m.doneN++
		if m.doneN >= len(m.queue) {
			m.mode = modeDone
			return m, nil
		}
		next := m.queue[m.doneN]
		m.current = next.Label
		return m, uninstallStep(next)

	case launchDoneMsg:
		if msg.err != nil {
			m.flash = "could not open: " + msg.err.Error()
		} else {
			m.flash = "opened " + msg.label + " on the device"
		}
		return m, nil

	case tea.KeyMsg:
		// Any key dismisses the previous action's message.
		m.flash = ""
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeHelp:
			m.mode = modeBrowse // any key closes the overlay
			return m, nil
		case modeRunning:
			return m, nil // ignore input while removals are in flight
		case modeDone:
			m.apps = removeUninstalled(m.apps, m.queue, m.results)
			for _, a := range m.queue {
				delete(m.selected, a.Pkg)
			}
			m.queue = nil
			m.results = nil
			m.doneN = 0
			m.current = ""
			m.mode = modeBrowse
			m.rebuild()
			return m, nil
		default:
			return m.updateBrowse(msg)
		}
	}
	return m, nil
}

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		m.mode = modeBrowse
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.rebuild()
		}
	default:
		if len(msg.Runes) > 0 {
			m.filter += string(msg.Runes)
			m.rebuild()
		}
	}
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		cmd := m.startUninstall()
		return m, cmd
	case "up", "k":
		m.confirmScroll = max(0, m.confirmScroll-1)
	case "down", "j":
		m.confirmScroll = min(max(0, len(m.selectedApps())-m.listHeight()), m.confirmScroll+1)
	default:
		m.mode = modeBrowse
		m.confirmScroll = 0
	}
	return m, nil
}

func (m Model) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "right", "l", "tab":
		m.switchTab(1)
	case "left", "h", "shift+tab":
		m.switchTab(-1)

	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-m.listHeight())
	case "pgdown":
		m.moveCursor(m.listHeight())
	case "home", "g":
		m.cur().cursor = 0
		m.clampOffset()
	case "end", "G":
		m.cur().cursor = max(0, len(m.cur().apps)-1)
		m.clampOffset()

	case " ":
		if a := m.currentApp(); a != nil {
			inSelected := m.cur().name == selTab
			if m.selected[a.Pkg] {
				delete(m.selected, a.Pkg)
			} else {
				m.selected[a.Pkg] = true
			}
			m.rebuild() // the selected view's contents just changed
			if inSelected {
				// Unmarking here removes the row, so the list shifts up under
				// the cursor. Advancing would skip the next entry.
				m.clampOffset()
			} else {
				m.moveCursor(1)
			}
		}

	case "a":
		// Toggle the whole tab: select all unless they already all are.
		t := m.cur()
		allSelected := len(t.apps) > 0
		for _, a := range t.apps {
			if !m.selected[a.Pkg] {
				allSelected = false
				break
			}
		}
		for _, a := range t.apps {
			if allSelected {
				delete(m.selected, a.Pkg)
			} else {
				m.selected[a.Pkg] = true
			}
		}
		m.rebuild()

	case "u":
		m.selected = map[string]bool{}
		m.rebuild()

	case "/":
		m.mode = modeFilter

	case "o", "enter":
		// Open the app on the device, so you can remind yourself what it is
		// before deciding to remove it.
		if a := m.currentApp(); a != nil {
			m.flash = "opening " + a.Label + "..."
			return m, launchCmd(a.Pkg, a.Label)
		}

	case "?":
		m.mode = modeHelp

	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}

	case "d":
		if len(m.selected) > 0 {
			m.mode = modeConfirm
			m.confirmScroll = 0
		}
	}
	return m, nil
}

func (m *Model) switchTab(delta int) {
	if len(m.tabs) == 0 {
		return
	}
	// Wrap around; with a dozen categories, cycling beats hitting a wall.
	m.active = (m.active + delta + len(m.tabs)) % len(m.tabs)
	m.clampOffset()
}

func (m Model) currentApp() *adb.App {
	t := m.tabs[m.active]
	if t.cursor < 0 || t.cursor >= len(t.apps) {
		return nil
	}
	return t.apps[t.cursor]
}

func (m *Model) moveCursor(delta int) {
	t := m.cur()
	if len(t.apps) == 0 {
		return
	}
	t.cursor = clamp(t.cursor+delta, 0, len(t.apps)-1)
	m.clampOffset()
}

func (m *Model) clampOffset() {
	t := m.cur()
	h := m.listHeight()
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+h {
		t.offset = t.cursor - h + 1
	}
	// Cap at len-h, not len-1: the last screenful should sit flush at the bottom
	// rather than scrolling into empty space. Reachable by growing the window,
	// which raises h while offset stays put.
	t.offset = clamp(t.offset, 0, max(0, len(t.apps)-h))
}

// ---------- rendering ----------

func (m Model) rule() string { return ruleStyle.Render(strings.Repeat("─", m.width)) }

func (m Model) header() string {
	left := appTitle.Render("adb-triage")
	right := deviceStyle.Render(fmt.Sprintf("%s  ·  %d apps  ·  %s",
		m.device.Model, len(m.apps), humanMB(totalSize(m.apps))))
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// sidebarLines renders the category column, one category per row. A horizontal
// tab strip was tried first and does not survive 19 categories: it wraps to
// three lines of undelimited text that reads as a wall. A vertical column gives
// each category its own row and keeps the count aligned.
//
// The column scrolls if there are more categories than rows available.
func (m Model) sidebarLines(h int) []string {
	// Keep the active category on screen.
	offset := 0
	if len(m.tabs) > h {
		offset = clamp(m.active-h/2, 0, len(m.tabs)-h)
	}

	lines := make([]string, 0, h)
	for i := offset; i < len(m.tabs) && len(lines) < h; i++ {
		t := m.tabs[i]
		marked := m.markedIn(t)

		count := fmt.Sprint(len(t.apps))
		// Every row in the selected view is by definition marked, so "5/5"
		// there is noise rather than information.
		if marked > 0 && t.name != selTab {
			count = fmt.Sprintf("%d/%d", marked, len(t.apps))
		}

		// Dot leader: the dots absorb variation in BOTH the name and the count
		// lengths, so every row is exactly sidebarWidth wide and every count
		// ends in the same column. Every branch below must build the same
		// widths, or the divider between sidebar and list goes ragged.
		name := truncate(t.name, sidebarWidth-9)
		fill := max(1, sidebarWidth-3-lipgloss.Width(name)-len(count))
		dots := strings.Repeat("·", fill)

		var body string
		switch {
		case i == m.active:
			body = sideActive.Render(fmt.Sprintf(" %s%s %s ",
				name, strings.Repeat(" ", fill), count))
		case marked > 0:
			body = " " + sideMarked.Render(name) + dim.Render(dots) +
				" " + sideMarked.Render(count) + " "
		default:
			body = " " + sideIdle.Render(name) + dim.Render(dots) +
				" " + dim.Render(count) + " "
		}
		lines = append(lines, padTo(body, sidebarWidth))
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", sidebarWidth))
	}
	return lines
}

// listLines renders the app rows for the active category.
func (m Model) listLines(h int) []string {
	t := m.tabs[m.active]
	lines := make([]string, 0, h)

	if len(t.apps) == 0 {
		lines = append(lines, dim.Render("  nothing in this category"))
	}
	end := min(t.offset+h, len(t.apps))
	for i := t.offset; i < end && len(lines) < h; i++ {
		lines = append(lines, m.appRow(t.apps[i], i == t.cursor, m.selected[t.apps[i].Pkg]))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// body glues the sidebar and the list together column-wise, with a vertical
// divider between them.
func (m Model) body(h int) string {
	side := m.sidebarLines(h)
	list := m.listLines(h)

	var b strings.Builder
	for i := 0; i < h; i++ {
		b.WriteString(side[i])
		b.WriteString(ruleStyle.Render("│"))
		b.WriteString(list[i])
		b.WriteString("\n")
	}
	return b.String()
}

// appRow formats one app line. labelW adapts to the terminal so the package
// name column doesn't get squeezed out on narrow windows. Labels we guessed
// from the package name are rendered dim and italic, so it's visible at a
// glance which names are real and which are filler.
func (m Model) appRow(a *adb.App, cursor, checked bool) string {
	labelW := clamp(m.width-sidebarWidth-45, 14, 38)

	prefix := "  "
	if cursor {
		prefix = rowCursor.Render(" ▸")
	}
	box := dim.Render("[ ]")
	if checked {
		box = rowChecked.Render("[x]")
	}

	label := padTo(truncate(a.Label, labelW), labelW)
	switch {
	case cursor:
		label = rowActive.Render(label)
	case !m.known[a.Pkg]:
		label = guessStyle.Render(label)
	}

	return fmt.Sprintf("%s%s %s %s  %s",
		prefix, box, label,
		sizeStyle.Render(fmt.Sprintf("%8s", humanMB(a.SizeMB))),
		dim.Render(a.Pkg))
}

// helpBlock renders the key bindings as an aligned grid instead of one long
// run-on line.
func (m Model) helpBlock() string {
	type binding struct{ key, desc string }
	rows := [][]binding{
		{{"←/→", "category"}, {"↑/↓", "navigate"}, {"space", "mark"}, {"a", "mark all"}, {"o", "open"}},
		{{"/", "filter"}, {"u", "clear"}, {"d", "uninstall"}, {"?", "help"}, {"q", "quit"}},
	}
	colW := max(14, m.width/5)

	var out []string
	for _, row := range rows {
		var line strings.Builder
		for _, b := range row {
			cell := keyStyle.Render(b.key) + " " + dim.Render(b.desc)
			line.WriteString(padTo(cell, colW))
		}
		out = append(out, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(out, "\n")
}

func (m Model) View() string {
	switch m.mode {
	case modeConfirm:
		return m.confirmView()
	case modeHelp:
		return m.helpView()
	case modeRunning:
		return m.runningView()
	case modeDone:
		return m.doneView()
	}
	return m.browseView()
}

// helpView is the full-screen overlay behind "?". It documents the keys and,
// just as importantly, explains where the app names come from, since a dim
// italic label in the list is otherwise unexplained.
func (m Model) helpView() string {
	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"Navigate", [][2]string{
			{"← → h l Tab", "switch category"},
			{"↑ ↓ j k", "move through the list"},
			{"PgUp PgDn", "jump one screen"},
			{"g G", "start / end of the list"},
		}},
		{"Select", [][2]string{
			{"space", "mark or unmark the app under the cursor"},
			{"a", "mark the whole category (unmarks if all are already marked)"},
			{"u", "clear everything marked"},
		}},
		{"Filter", [][2]string{
			{"/", "filter by name or package"},
			{"esc", "clear the filter"},
		}},
		{"Act", [][2]string{
			{"o  enter", "open the app on the device"},
			{"d", "review and uninstall what's marked"},
			{"q", "quit without changing anything"},
		}},
	}

	var b strings.Builder
	b.WriteString(appTitle.Render(" HELP ") + "\n")
	b.WriteString(m.rule() + "\n")

	for _, s := range sections {
		b.WriteString("\n   " + rowActive.Render(s.title) + "\n")
		for _, r := range s.rows {
			b.WriteString(fmt.Sprintf("     %s %s\n",
				padTo(keyStyle.Render(r[0]), 16), dim.Render(r[1])))
		}
	}

	b.WriteString("\n" + m.rule() + "\n")
	fmt.Fprintf(&b, "   %s\n", dim.Render(fmt.Sprintf(
		"%d apps ship with a real name built into the binary. A dim italic label",
		classify.SeedSize())))
	b.WriteString("   " + dim.Render(
		"is guessed from the package name; adding it to seed.json fixes it.") + "\n\n")
	b.WriteString("   " + dim.Render("any key to go back"))
	return b.String()
}

func (m Model) browseView() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.rule() + "\n")
	b.WriteString(m.body(m.listHeight()))
	b.WriteString(m.rule() + "\n")

	if m.mode == modeFilter {
		b.WriteString(keyStyle.Render(" filter: ") + m.filter + "▌\n\n")
		b.WriteString(dim.Render(" enter or esc to go back"))
		return b.String()
	}

	t := m.tabs[m.active]
	left := fmt.Sprintf(" %s marked · %s",
		rowChecked.Render(fmt.Sprint(len(m.selected))), humanMB(m.selectedSize()))
	right := dim.Render(fmt.Sprintf("%s · %d apps · %s ", t.name, len(t.apps), humanMB(t.sizeMB)))
	if m.filter != "" {
		right = dim.Render(fmt.Sprintf("filter %q · ", m.filter)) + right
	}
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")

	// The flash line reports the last action. It keeps its row even when empty,
	// so the layout below it does not shift as messages come and go.
	if m.flash != "" {
		b.WriteString(okStyle.Render(" "+truncate(m.flash, m.width-2)) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(m.helpBlock())
	return b.String()
}

// confirmView is the full-screen review of exactly what is about to be
// uninstalled. Uninstalls are irreversible, so this always lists the packages
// rather than only counting them.
func (m Model) confirmView() string {
	apps := m.selectedApps()

	var b strings.Builder
	b.WriteString(dangerStyle.Render(" CONFIRM UNINSTALL ") + "\n")
	b.WriteString(m.rule() + "\n\n")
	fmt.Fprintf(&b, "   %s apps will be removed, freeing %s:\n\n",
		dangerStyle.Render(fmt.Sprint(len(apps))),
		dangerStyle.Render(humanMB(totalSize(apps))))

	h := m.listHeight()
	// Same bound updateConfirm clamps confirmScroll to, so the two agree.
	start := clamp(m.confirmScroll, 0, max(0, len(apps)-h))
	end := min(start+h, len(apps))
	for i := start; i < end; i++ {
		a := apps[i]
		b.WriteString(fmt.Sprintf("   %s %s  %s\n",
			padTo(truncate(a.Label, 28), 28),
			sizeStyle.Render(fmt.Sprintf("%8s", humanMB(a.SizeMB))),
			dim.Render(a.Pkg)))
	}
	for i := end - start; i < h; i++ {
		b.WriteString("\n")
	}
	if end < len(apps) {
		b.WriteString(dim.Render(fmt.Sprintf("   ...and %d more below (↑/↓ scrolls)", len(apps)-end)) + "\n")
	} else {
		b.WriteString("\n")
	}

	b.WriteString(m.rule() + "\n")
	b.WriteString(dangerStyle.Render("   Each app's local data goes with it. This cannot be undone.") + "\n\n")
	b.WriteString("   " + keyStyle.Render("y") + dim.Render(" confirm and uninstall") +
		"      " + keyStyle.Render("any other key") + dim.Render(" cancel"))
	return b.String()
}

// runningView shows removal progress. adb uninstall can be slow (seconds each
// on some devices), so live progress is the difference between "working" and
// "frozen" from the user's side.
func (m Model) runningView() string {
	total := len(m.queue)

	var b strings.Builder
	b.WriteString(appTitle.Render(" UNINSTALLING ") + "\n")
	b.WriteString(m.rule() + "\n\n")

	barW := clamp(m.width-20, 10, 50)
	filled := 0
	if total > 0 {
		filled = m.doneN * barW / total
	}
	bar := okStyle.Render(strings.Repeat("█", filled)) +
		ruleStyle.Render(strings.Repeat("░", barW-filled))
	fmt.Fprintf(&b, "   %s  %d/%d\n\n", bar, m.doneN, total)
	b.WriteString("   " + dim.Render("removing now: ") + rowActive.Render(m.current) + "\n\n")

	// Show the tail of what's been done, newest last, filling the body.
	h := m.listHeight() - 2
	start := max(0, len(m.results)-h)
	for _, r := range m.results[start:] {
		b.WriteString("   " + resultStyle(r).Render(truncate(r.text, m.width-4)) + "\n")
	}
	return b.String()
}

func (m Model) doneView() string {
	var b strings.Builder
	var failed int
	for _, r := range m.results {
		if r.failed {
			failed++
		}
	}

	b.WriteString(appTitle.Render(" RESULT ") + "\n")
	b.WriteString(m.rule() + "\n\n")
	for _, r := range m.results {
		b.WriteString("   " + resultStyle(r).Render(r.text) + "\n")
	}
	b.WriteString("\n" + m.rule() + "\n")
	fmt.Fprintf(&b, "   %d removed, %d failed\n\n", len(m.results)-failed, failed)
	b.WriteString("   " + dim.Render("any key to go back"))
	return b.String()
}

func resultStyle(r uninstallStepMsg) lipgloss.Style {
	if r.failed {
		return dangerStyle
	}
	return okStyle
}

// ---------- small helpers ----------

// truncate cuts to n display columns, respecting rune boundaries.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > n {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// padTo right-pads to n display columns, measuring rendered width so styled
// text and wide runes still line up.
func padTo(s string, n int) string {
	if pad := n - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// humanMB is a package-local alias for adb.HumanMB, kept short because it's
// called from nearly every render line.
func humanMB(mb int64) string { return adb.HumanMB(mb) }
