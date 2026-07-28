// Package adb wraps the adb CLI. Every function shells out to `adb`, so the
// binary must be on PATH and a device must be authorized.
package adb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Timeouts for the adb calls behind each exported function. Every one of these
// shells out to a device that can wedge, unplug mid-command, or sit at a locked
// screen, and adb itself will wait indefinitely in all three cases. Without a
// deadline the TUI hangs with no way out short of killing the process.
//
// Uninstall gets far longer than the reads: removing a large app is genuinely
// slow on some devices, and killing a half-finished uninstall is worse than
// waiting.
const (
	ReadTimeout      = 30 * time.Second
	LaunchTimeout    = 30 * time.Second
	UninstallTimeout = 2 * time.Minute
)

// App is one installed package plus whatever metadata we could resolve.
type App struct {
	Pkg        string
	Label      string // friendly name, filled in by the classifier
	Category   string
	SizeMB     int64
	Launchable bool
}

// Device is an entry from `adb devices -l`.
type Device struct {
	Serial string
	Model  string
}

func run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "adb", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A killed process reports a generic exit error, so surface the deadline
		// explicitly. "signal: killed" alone points at the wrong problem.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return string(out), fmt.Errorf("adb %s: %w", strings.Join(args, " "), ctxErr)
		}
		return string(out), fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Devices lists attached devices in the `device` state. Devices that are
// unauthorized or offline are skipped, since we can't query them anyway.
func Devices(ctx context.Context) ([]Device, error) {
	out, err := run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	var devs []Device
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Scan() // skip "List of devices attached"
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		d := Device{Serial: fields[0]}
		for _, f := range fields[2:] {
			if model, ok := strings.CutPrefix(f, "model:"); ok {
				d.Model = model
			}
		}
		devs = append(devs, d)
	}
	return devs, sc.Err()
}

// ThirdParty returns packages the user installed (pm list packages -3).
func ThirdParty(ctx context.Context) ([]string, error) {
	out, err := run(ctx, "shell", "pm", "list", "packages", "-3")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		// CutPrefix rather than TrimPrefix: a line without the "package:" prefix
		// is adb chatter (daemon notices), not a package name.
		if p, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "package:"); ok && p != "" {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs, sc.Err()
}

// Launchable returns the set of packages that have an icon in the app drawer.
// Anything missing from this set is a background service with no UI.
func Launchable(ctx context.Context) (map[string]bool, error) {
	out, err := run(ctx, "shell", "cmd", "package", "query-activities", "--brief",
		"-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		slash := strings.Index(line, "/")
		if slash <= 0 || strings.ContainsAny(line[:slash], " \t") {
			continue
		}
		set[line[:slash]] = true
	}
	return set, sc.Err()
}

// Sizes returns per-package disk usage in MB, summing the APK and its data
// directory. Note this does NOT include media under /sdcard/Android/media,
// which for apps like WhatsApp dwarfs everything dumpsys reports.
func Sizes(ctx context.Context) (map[string]int64, error) {
	out, err := run(ctx, "shell", "dumpsys", "diskstats")
	if err != nil {
		return nil, err
	}
	names, err := jsonArray[string](out, "Package Names:")
	if err != nil {
		return nil, err
	}
	appSizes, err := jsonArray[int64](out, "App Sizes:")
	if err != nil {
		return nil, err
	}
	dataSizes, err := jsonArray[int64](out, "App Data Sizes:")
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	for i, n := range names {
		var total int64
		if i < len(appSizes) {
			total += appSizes[i]
		}
		if i < len(dataSizes) {
			total += dataSizes[i]
		}
		sizes[n] = total / (1024 * 1024)
	}
	return sizes, nil
}

// jsonArray pulls a `label: [...]` line out of dumpsys output and decodes it.
func jsonArray[T any](out, label string) ([]T, error) {
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // dumpsys lines are huge
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(strings.TrimSpace(line), label) {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), label))
		var vals []T
		if err := json.Unmarshal([]byte(raw), &vals); err != nil {
			return nil, fmt.Errorf("decoding %q from diskstats: %w", label, err)
		}
		return vals, nil
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%q not found in diskstats output", label)
}

// HumanMB formats a megabyte count for display: MB below a gigabyte, GB above,
// with one decimal place so 1536 MB reads "1.5 GB" instead of a rounded "2 GB".
// Sizes are stored in MB throughout; this only touches presentation.
func HumanMB(mb int64) string {
	if mb < 1024 {
		return fmt.Sprintf("%d MB", mb)
	}
	return fmt.Sprintf("%.1f GB", float64(mb)/1024)
}

// Launch starts an app on the device. It resolves the launcher activity first
// and starts it explicitly; if that fails it falls back to monkey, which some
// apps respond to when resolve-activity comes back empty.
func Launch(ctx context.Context, pkg string) error {
	out, err := run(ctx, "shell", "cmd", "package", "resolve-activity", "--brief",
		"-c", "android.intent.category.LAUNCHER", pkg)
	if err == nil {
		if comp := lastComponent(out, pkg); comp != "" {
			if _, err := run(ctx, "shell", "am", "start", "-n", comp); err == nil {
				return nil
			}
		}
	}

	out2, err2 := run(ctx, "shell", "monkey", "-p", pkg,
		"-c", "android.intent.category.LAUNCHER", "1")
	if err2 == nil && strings.Contains(out2, "Events injected: 1") {
		return nil
	}
	return fmt.Errorf("could not open %s (it may have no UI)", pkg)
}

// lastComponent picks the "pkg/activity" line out of resolve-activity output,
// which prefixes the component with metadata lines on some Android versions.
func lastComponent(out, pkg string) string {
	var comp string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, pkg+"/") {
			comp = line
		}
	}
	return comp
}

// Uninstall removes a package for the current user. It tries the plain
// uninstall first and falls back to `pm uninstall --user 0`, which works for
// packages that are preinstalled but updatable.
func Uninstall(ctx context.Context, pkg string) error {
	out, err := run(ctx, "uninstall", pkg)
	if err == nil && strings.Contains(out, "Success") {
		return nil
	}
	out2, err2 := run(ctx, "shell", "pm", "uninstall", "--user", "0", pkg)
	if err2 == nil && strings.Contains(out2, "Success") {
		return nil
	}
	return fmt.Errorf("uninstall %s failed: %s / %s",
		pkg, strings.TrimSpace(out), strings.TrimSpace(out2))
}
