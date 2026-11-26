package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStatusConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultStatusesHaveNoStyle(t *testing.T) {
	for _, def := range DefaultStatuses() {
		if def.Color != "" || def.Icon != "" {
			t.Errorf("default status %q has style: %#v", def.ID, def)
		}
	}
	if warnings := Default().Warnings; len(warnings) != 0 {
		t.Errorf("default config warnings = %#v", warnings)
	}
}

func TestParseStatusColorGrammar(t *testing.T) {
	valid := map[string]string{
		"":               "",
		"auto":           "auto",
		" AUTO ":         "auto",
		"blue":           "blue",
		"BRIGHT-Cyan":    "bright-cyan",
		"bright-black":   "bright-black",
		"#0f0":           "#0f0",
		"#AABBCC":        "#aabbcc",
		"ansi256:0":      "ansi256:0",
		"ansi256:255":    "ansi256:255",
		"ansi256:007":    "ansi256:7",
		"  ansi256:42  ": "ansi256:42",
	}
	for value, want := range valid {
		if got, ok := ParseStatusColor(value); !ok || got != want {
			t.Errorf("ParseStatusColor(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}

	invalid := []string{
		"chartreuse", "#12", "#12345", "#GGG000", "ansi256:", "ansi256:256",
		"ansi256:-1", "ansi256:1.5", "bright-", "bright-pink", "rgb(1,2,3)",
		"#", "ansi256",
	}
	for _, value := range invalid {
		if got, ok := ParseStatusColor(value); ok {
			t.Errorf("ParseStatusColor(%q) = %q, want invalid", value, got)
		}
	}
}

func TestStatusColorANSIIndices(t *testing.T) {
	cases := map[string][2]int{
		"blue":           {4, 12},
		"black":          {0, 8},
		"white":          {7, 15},
		"bright-red":     {9, 9},
		"bright-magenta": {13, 13},
	}
	for value, want := range cases {
		light, dark, ok := StatusColorANSIIndices(value)
		if !ok || light != want[0] || dark != want[1] {
			t.Errorf("StatusColorANSIIndices(%q) = %d/%d, %v; want %d/%d", value, light, dark, ok, want[0], want[1])
		}
	}
	if _, _, ok := StatusColorANSIIndices("chartreuse"); ok {
		t.Error("StatusColorANSIIndices accepted an unknown name")
	}
}

func TestLoadStatusStyles(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "in_progress", "label": "In Progress", "color": "auto"},
			{"id": "blocked", "label": "Blocked", "color": "bright-red", "icon": "■"},
			{"id": "in_review", "label": "In Review", "color": "#808080"},
			{"id": "ready", "label": "Ready", "color": "ansi256:245", "icon": " ✓ "},
			{"id": "done", "label": "Done"}
		],
		"default_status": "ready"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings = %#v", cfg.Warnings)
	}
	want := []StatusDef{
		{ID: StatusInProgress, Label: "In Progress", Color: "auto"},
		{ID: StatusBlocked, Label: "Blocked", Color: "bright-red", Icon: "■"},
		{ID: StatusInReview, Label: "In Review", Color: "#808080"},
		{ID: StatusReady, Label: "Ready", Color: "ansi256:245", Icon: " ✓ "},
		{ID: StatusDone, Label: "Done"},
	}
	for i, def := range cfg.Statuses {
		if def != want[i] {
			t.Errorf("status %d = %#v, want %#v", i, def, want[i])
		}
	}
	if got := cfg.Statuses[3].ResolvedIcon(); got != "✓" {
		t.Errorf("ResolvedIcon = %q, want trimmed ✓", got)
	}
}

func TestLoadInvalidStatusStylesWarnAndPreserve(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "todo", "label": "Todo", "color": "chartreuse", "icon": "ab"},
			{"id": "doing", "label": "Doing", "color": "ansi256:300", "icon": "\u001b[31m"},
			{"id": "done", "label": "Done"}
		],
		"default_status": "todo"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("invalid cosmetic values must not fail the load: %v", err)
	}
	// Raw values stay in config.json so a later Save cannot silently fix them;
	// the TUI resolves the fallback at render time.
	if got := cfg.Statuses[0]; got.Color != "chartreuse" || got.Icon != "ab" {
		t.Errorf("todo raw values = %#v", got)
	}
	if got := cfg.Statuses[1]; got.Color != "ansi256:300" || got.Icon != "\x1b[31m" {
		t.Errorf("doing raw values = %#v", got)
	}
	if got := cfg.Statuses[2]; got.Color != "" || got.Icon != "" {
		t.Errorf("untouched status = %#v", got)
	}
	for i := 0; i < 2; i++ {
		if got := cfg.Statuses[i].ResolvedIcon(); got != "" {
			t.Errorf("status %d resolved icon = %q, want empty", i, got)
		}
	}
	if len(cfg.Warnings) != 4 {
		t.Fatalf("warnings = %#v, want four", cfg.Warnings)
	}
	var colorWarnings, iconWarnings int
	for _, warning := range cfg.Warnings {
		if strings.Contains(warning, "\x1b") {
			t.Errorf("warning contains a raw escape: %q", warning)
		}
		if strings.Contains(warning, "invalid color") {
			colorWarnings++
		}
		if strings.Contains(warning, "invalid icon") {
			iconWarnings++
		}
	}
	if colorWarnings != 2 || iconWarnings != 2 {
		t.Errorf("warnings = %#v", cfg.Warnings)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), `status "todo"`) {
		t.Errorf("warnings do not name the status: %#v", cfg.Warnings)
	}
}

func TestStatusIconSanitization(t *testing.T) {
	valid := map[string]string{
		" ✓ ": "✓",
		"中":   "中",
		"é":  "é",
		"⚠️":  "⚠️",
	}
	for value, want := range valid {
		got, ok := sanitizeStatusIcon(value)
		if !ok || got != want {
			t.Errorf("sanitizeStatusIcon(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}

	invalid := []string{
		"ab", "中文", "\t", "\x1b", "\x7f", "\u0085", "́", " ",
	}
	for _, value := range invalid {
		if got, ok := sanitizeStatusIcon(value); ok {
			t.Errorf("sanitizeStatusIcon(%q) = %q, want invalid", value, got)
		}
	}
}

func TestStatusDefResolvedIcon(t *testing.T) {
	cases := map[string]string{
		"":         "",
		" ✓ ":      "✓",
		"中":        "中",
		"ab":       "",
		"\x1b[31m": "",
		"中文":       "",
	}
	for raw, want := range cases {
		if got := (StatusDef{Icon: raw}).ResolvedIcon(); got != want {
			t.Errorf("StatusDef{Icon: %q}.ResolvedIcon() = %q, want %q", raw, got, want)
		}
	}
}

func TestContrastWarnings(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "low", "label": "Low", "color": "#4ADE80"},
			{"id": "safe", "label": "Safe", "color": "#808080"},
			{"id": "darklow", "label": "Dark Low", "color": "ansi256:16"},
			{"id": "named", "label": "Named", "color": "green"},
			{"id": "ansi002", "label": "Ansi 2", "color": "ansi256:2"}
		],
		"default_status": "low"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(joined, `status "low"`) || !strings.Contains(joined, "light background") {
		t.Errorf("missing light-background warning: %#v", cfg.Warnings)
	}
	if !strings.Contains(joined, `status "darklow"`) || !strings.Contains(joined, "dark background") {
		t.Errorf("missing dark-background warning: %#v", cfg.Warnings)
	}
	for _, name := range []string{`"safe"`, `"named"`, `"ansi002"`} {
		if strings.Contains(joined, name) {
			t.Errorf("%s should not warn: %#v", name, cfg.Warnings)
		}
	}
}

func TestSavePreservesInvalidStyle(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "todo", "label": "Todo", "color": "chartreuse", "icon": "ab"},
			{"id": "done", "label": "Done"}
		],
		"default_status": "todo"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("warnings = %#v", cfg.Warnings)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	got := reloaded.Statuses[0]
	if got.Color != "chartreuse" || got.Icon != "ab" {
		t.Errorf("Save rewrote the invalid style: %#v", got)
	}
	if len(reloaded.Warnings) != 2 {
		t.Errorf("warnings did not repeat after reload: %#v", reloaded.Warnings)
	}
}

func TestOldConfigLoadsUnchanged(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "todo", "label": "Todo"},
			{"id": "done", "label": "Done"}
		],
		"default_status": "todo"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("old config produced warnings: %#v", cfg.Warnings)
	}
	for _, def := range cfg.Statuses {
		if def.Color != "" || def.Icon != "" {
			t.Errorf("old config gained style: %#v", def)
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"color"`, `"icon"`, `"warnings"`} {
		if strings.Contains(string(data), field) {
			t.Errorf("serialized config contains %s: %s", field, data)
		}
	}
}

func TestRegisterProjectKeepsStyleWarnings(t *testing.T) {
	writeStatusConfig(t, `{
		"statuses": [
			{"id": "todo", "label": "Todo", "color": "chartreuse"},
			{"id": "done", "label": "Done"}
		],
		"default_status": "todo"
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %#v", cfg.Warnings)
	}
	if _, _, err := cfg.RegisterProject("/code/a", "a", "", ""); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Errorf("RegisterProject dropped or duplicated warnings: %#v", cfg.Warnings)
	}
	if got := cfg.Statuses[0].Color; got != "chartreuse" {
		t.Errorf("RegisterProject rewrote the invalid color: %q", got)
	}
}
