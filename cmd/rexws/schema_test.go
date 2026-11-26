package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/agentstate"
	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// releasedFixtureHashes pins every frozen contract fixture. Editing or deleting
// a fixture fails until this map is updated deliberately in the same change,
// so a shape change cannot be slipped in by regenerating the file alone.
var releasedFixtureHashes = map[int]string{
	1: "feccc77104aa0b39d210c130e690c70af67e0badf96db03a608e60903609c2ef",
}

// schemaExamples maps each command to its illustrative document. The examples
// are canonical: each must equal a marshal of its own payload type.
var schemaExamples = []struct {
	file    string
	command string
}{
	{"list.json", "list"},
	{"status.json", "status"},
	{"switch.json", "switch"},
	{"new.json", "new"},
	{"rm.json", "rm"},
	{"project-add.json", "project add"},
	{"here.json", "here"},
	{"cleanup.json", "cleanup"},
}

// schemaChange is one difference between two manifests. Breaking changes
// require a schema_version bump; additive changes do not.
type schemaChange struct {
	Breaking    bool
	Description string
}

func (c schemaChange) String() string {
	if c.Breaking {
		return "breaking: " + c.Description
	}
	return "additive: " + c.Description
}

// generatedManifest returns the manifest and fails if building it fails.
func generatedManifest(t *testing.T) schemaManifest {
	t.Helper()
	manifest, err := buildSchemaManifest()
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return manifest
}

func loadManifest(t *testing.T, path string) schemaManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var manifest schemaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return manifest
}

func frozenManifestPath(version int) string {
	return filepath.Join("testdata", "schema", fmt.Sprintf("v%d.json", version))
}

// TestSchemaManifestGolden pins the whole contract against the bytes the CLI
// actually writes. Any shape change fails until the fixture, its pinned hash,
// and docs are updated deliberately. The failure names breaking changes
// separately, because those require a schema_version bump.
func TestSchemaManifestGolden(t *testing.T) {
	stdout, stderr, err := runCLI(t, "schema")
	if err != nil {
		t.Fatalf("schema: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("schema wrote stderr: %q", stderr)
	}
	var current schemaManifest
	if err := json.Unmarshal([]byte(stdout), &current); err != nil {
		t.Fatalf("parse schema output: %v", err)
	}

	path := frozenManifestPath(current.SchemaVersion)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no frozen contract for schema_version %d (%s): %v\nA version bump must add %s; never edit an older fixture.",
			current.SchemaVersion, path, err, path)
	}
	if bytes.Equal([]byte(stdout), data) {
		return
	}

	var frozen schemaManifest
	if err := json.Unmarshal(data, &frozen); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if frozen.SchemaVersion != current.SchemaVersion {
		t.Fatalf("%s declares schema_version %d, want %d", path, frozen.SchemaVersion, current.SchemaVersion)
	}
	changes := classifySchemaChange(frozen, current)
	var breaking, additive []string
	for _, change := range changes {
		if change.Breaking {
			breaking = append(breaking, change.Description)
		} else {
			additive = append(additive, change.Description)
		}
	}
	if len(breaking) > 0 {
		t.Fatalf("breaking JSON contract change while schema_version is still %d:\n  %s\nBump SchemaVersion, add %s, and keep %s unchanged.",
			current.SchemaVersion, strings.Join(breaking, "\n  "), frozenManifestPath(current.SchemaVersion+1), path)
	}
	if len(additive) > 0 {
		t.Fatalf("JSON contract shape changed (additive), schema_version %d:\n  %s\nUpdate %s, its pinned hash, docs/json-schema.md, and the examples.",
			current.SchemaVersion, strings.Join(additive, "\n  "), path)
	}
	t.Fatalf("%s does not match the CLI output but has no structural difference; restore the fixture.", path)
}

// TestReleasedFixturesArePinned makes every frozen fixture immutable in
// practice: editing or deleting one fails until the pinned hash is updated in
// the same reviewable change.
func TestReleasedFixturesArePinned(t *testing.T) {
	// Every version from 1 through the current one must stay pinned, even after
	// a bump. Deleting a superseded fixture is a contract change, not cleanup.
	for version := 1; version <= SchemaVersion; version++ {
		if _, ok := releasedFixtureHashes[version]; !ok {
			t.Errorf("schema_version %d has no pinned fixture hash; add it to releasedFixtureHashes", version)
		}
	}
	for version := range releasedFixtureHashes {
		if version < 1 || version > SchemaVersion {
			t.Errorf("releasedFixtureHashes pins v%d outside 1..%d", version, SchemaVersion)
		}
	}
	for version, want := range releasedFixtureHashes {
		data, err := os.ReadFile(frozenManifestPath(version))
		if err != nil {
			t.Fatalf("released fixture v%d: %v", version, err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(data))
		if got != want {
			t.Fatalf("released fixture v%d changed: sha256 %s, want %s\nA fixture edit must update releasedFixtureHashes and must be reviewed as a contract change.",
				version, got, want)
		}
	}
	// A new fixture without a pin would bypass the check.
	fixtures, err := filepath.Glob(filepath.Join("testdata", "schema", "v*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(fixture), "v%d.json", &version); err != nil {
			t.Fatalf("unexpected fixture name %s: %v", fixture, err)
		}
		if _, ok := releasedFixtureHashes[version]; !ok {
			t.Errorf("fixture %s has no pinned hash; add it to releasedFixtureHashes", fixture)
		}
	}
}

// TestSchemaCompatibility compares the generated manifest with the latest
// released fixture. At the same version no breaking change may appear; a higher
// version is the deliberate escape hatch and must carry its own pinned fixture.
func TestSchemaCompatibility(t *testing.T) {
	current := generatedManifest(t)
	latest := 0
	for version := range releasedFixtureHashes {
		if version > latest {
			latest = version
		}
	}
	if current.SchemaVersion < latest {
		t.Fatalf("schema_version regressed from %d to %d", latest, current.SchemaVersion)
	}
	frozen := loadManifest(t, frozenManifestPath(latest))
	changes := classifySchemaChange(frozen, current)
	if current.SchemaVersion == latest && hasBreaking(changes) {
		t.Fatalf("breaking change at released schema_version %d; bump SchemaVersion and add v%d: %v",
			latest, latest+1, changes)
	}
}

// TestSchemaEnumsMatchCanonicalSlices keeps the manifest enums tied to the
// canonical slices next to the Go constants, so adding a constant cannot leave
// the contract stale with green tests.
func TestSchemaEnumsMatchCanonicalSlices(t *testing.T) {
	manifest := loadManifest(t, frozenManifestPath(SchemaVersion))
	for _, tt := range []struct {
		typeName string
		want     []string
	}{
		{"State", stringValues(agentstate.Values())},
		{"CleanupClassification", stringValues(ops.CleanupClassifications())},
		{"CleanupBranchState", stringValues(ops.CleanupBranchStates())},
		{"CleanupAction", stringValues(ops.CleanupActions())},
		{"CleanupResult", stringValues(ops.CleanupResults())},
		{"CleanupOrphanKind", stringValues(ops.CleanupOrphanKinds())},
	} {
		got := manifest.Types[tt.typeName].Enum
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s enum = %v, want canonical %v", tt.typeName, got, tt.want)
		}
	}

	kind := fieldNamed(manifest.Types["CleanupItem"].Fields, "kind")
	if !reflect.DeepEqual(kind.Enum, ops.CleanupKinds()) {
		t.Errorf("CleanupItem.kind enum = %v, want canonical %v", kind.Enum, ops.CleanupKinds())
	}
}

func fieldNamed(fields []schemaField, name string) schemaField {
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	return schemaField{}
}

// TestContractCoversEveryJSONCommand fails when a Cobra command gains a --json
// flag but is not in contractCommands, or the reverse.
func TestContractCoversEveryJSONCommand(t *testing.T) {
	manifest := loadManifest(t, frozenManifestPath(SchemaVersion))
	documented := make(map[string]bool, len(manifest.Commands))
	for _, command := range manifest.Commands {
		documented[command.Name] = true
	}

	found := make(map[string]bool)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Flags().Lookup("json") != nil {
			found[strings.TrimPrefix(command.CommandPath(), "rexws ")] = true
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd())

	for name := range found {
		if !documented[name] {
			t.Errorf("command %q has --json but is missing from contractCommands", name)
		}
	}
	for name := range documented {
		if !found[name] {
			t.Errorf("contractCommands lists %q but no --json command exists", name)
		}
	}
}

// TestSchemaCommandNeedsNoConfig proves the manifest is static: no config
// directory, no Rex binary, no repository.
func TestSchemaCommandNeedsNoConfig(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv(config.EnvDir, configDir)
	t.Setenv(config.EnvRexBin, filepath.Join(t.TempDir(), "no-such-rex"))

	stdout, stderr, err := runCLI(t, "schema")
	if err != nil {
		t.Fatalf("schema: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("schema wrote stderr: %q", stderr)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("schema touched the config directory: %v", err)
	}
	var manifest schemaManifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("parse schema JSON %q: %v", stdout, err)
	}
	if manifest.Format != schemaFormat {
		t.Errorf("format = %q, want %q", manifest.Format, schemaFormat)
	}
	if manifest.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", manifest.SchemaVersion, SchemaVersion)
	}
	if len(manifest.Commands) != 8 {
		t.Errorf("commands = %d, want 8", len(manifest.Commands))
	}
	seen := map[string]bool{}
	for _, command := range manifest.Commands {
		if seen[command.Name] {
			t.Errorf("command %q appears twice", command.Name)
		}
		seen[command.Name] = true
		if command.SchemaVersion != SchemaVersion {
			t.Errorf("command %q schema_version = %d, want %d", command.Name, command.SchemaVersion, SchemaVersion)
		}
		if _, ok := manifest.Types[command.Root]; !ok {
			t.Errorf("command %q root %q has no type definition", command.Name, command.Root)
		}
	}
	for _, name := range []string{"list", "status", "switch", "new", "rm", "project add", "here", "cleanup"} {
		if !seen[name] {
			t.Errorf("manifest is missing command %q", name)
		}
	}
}

// TestSchemaExamplesValidate validates one golden example per command against
// the manifest. The examples are also decoded into the payload types where one
// exists, so a field removed from the type but left in the example fails.
func TestSchemaExamplesValidate(t *testing.T) {
	manifest := loadManifest(t, frozenManifestPath(SchemaVersion))
	for _, tt := range schemaExamples {
		t.Run(tt.command, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "schema", "examples", tt.file))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatalf("parse example: %v", err)
			}
			validateCommand(t, manifest, tt.command, value)
			assertExampleSchemaVersion(t, tt.command, value)
			if err := json.Unmarshal(data, payloadTarget(tt.command)); err != nil {
				t.Errorf("%s example does not decode into its payload type: %v", tt.command, err)
			}
		})
	}
}

// TestSchemaExamplesAreCanonical proves each example contains exactly the
// fields the producer type emits. A hand-written impossible state, such as
// detached: false for an omitempty field, fails here. Key order and whitespace
// are irrelevant; the comparison is on the decoded documents.
func TestSchemaExamplesAreCanonical(t *testing.T) {
	for _, tt := range schemaExamples {
		t.Run(tt.command, func(t *testing.T) {
			path := filepath.Join("testdata", "schema", "examples", tt.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			var before any
			if err := json.Unmarshal(data, &before); err != nil {
				t.Fatalf("decode example: %v", err)
			}
			target := payloadTarget(tt.command)
			if err := json.Unmarshal(data, target); err != nil {
				t.Fatalf("decode example into payload: %v", err)
			}
			encoded, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			var after any
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s has fields the producer would not emit:\n example: %s\n payload: %s", path, data, encoded)
			}
		})
	}
}

func assertExampleSchemaVersion(t *testing.T, command string, value any) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s example is not an object", command)
	}
	version, ok := object["schema_version"].(float64)
	if !ok || int(version) != SchemaVersion {
		t.Errorf("%s schema_version = %v, want %d", command, object["schema_version"], SchemaVersion)
	}
}

// payloadTarget returns a pointer to the payload type for one command.
func payloadTarget(command string) any {
	switch command {
	case "list":
		return &ListPayload{}
	case "status":
		return &StatusPayload{}
	case "switch":
		return &SwitchPayload{}
	case "new":
		return &NewPayload{}
	case "rm":
		return &RmPayload{}
	case "project add":
		return &ProjectAddPayload{}
	case "here":
		return &HerePayload{}
	case "cleanup":
		return &ops.CleanupReport{}
	default:
		panic("no payload type for command " + command)
	}
}

// TestSchemaPayloadsCarryVersion checks the tag and type of schema_version on
// every root document independently of the CLI.
func TestSchemaPayloadsCarryVersion(t *testing.T) {
	values := map[string]any{
		"list":        ListPayload{SchemaVersion: SchemaVersion},
		"status":      StatusPayload{SchemaVersion: SchemaVersion},
		"switch":      SwitchPayload{SchemaVersion: SchemaVersion},
		"new":         NewPayload{SchemaVersion: SchemaVersion},
		"rm":          RmPayload{SchemaVersion: SchemaVersion},
		"project add": ProjectAddPayload{SchemaVersion: SchemaVersion},
		"here":        HerePayload{SchemaVersion: SchemaVersion},
		"cleanup":     ops.CleanupReport{SchemaVersion: SchemaVersion},
	}
	for command, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		var object map[string]any
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if got, ok := object["schema_version"].(float64); !ok || int(got) != SchemaVersion {
			t.Errorf("%s schema_version = %v, want %d", command, object["schema_version"], SchemaVersion)
		}
	}
}

// TestProjectAddJSONCarriesVersion covers the one command whose JSON test
// previously lived only in the contract: project add.
func TestProjectAddJSONCarriesVersion(t *testing.T) {
	repo := testRepo(t)
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))

	stdout, stderr, err := runCLI(t, "project", "add", repo, "--json")
	if err != nil {
		t.Fatalf("project add: %v\nstderr: %s", err, stderr)
	}
	var payload ProjectAddPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", payload.SchemaVersion, SchemaVersion)
	}
	if !payload.Created || payload.Project.Name != "repo" {
		t.Errorf("payload = %#v", payload)
	}
}

// TestParseJSONTagUsesFieldNameForUnnamedTag covers json:",omitempty" and the
// hidden "-" tag.
func TestParseJSONTagUsesFieldNameForUnnamedTag(t *testing.T) {
	name, optional, ok := parseJSONTag(",omitempty", "Field")
	if !ok || name != "Field" || !optional {
		t.Errorf("parseJSONTag(\",omitempty\") = (%q, %t, %t), want (Field, true, true)", name, optional, ok)
	}
	if _, _, ok := parseJSONTag("-", "Field"); ok {
		t.Error("parseJSONTag(\"-\") should hide the field")
	}
	if name, _, ok := parseJSONTag("", "Field"); !ok || name != "Field" {
		t.Errorf("parseJSONTag(\"\") = (%q, %t), want Field", name, ok)
	}
}

type ContractEmbedded struct {
	Embedded string `json:"embedded"`
}

type ContractOuter struct {
	ContractEmbedded
	Own string `json:"own"`
}

// TestManifestFlattensEmbeddedStructs matches encoding/json: an embedded struct
// without a JSON name contributes its fields to the parent.
func TestManifestFlattensEmbeddedStructs(t *testing.T) {
	builder := &manifestBuilder{
		types: make(map[string]schemaType),
		seen:  make(map[reflect.Type]bool),
		names: make(map[string]reflect.Type),
	}
	fields, err := builder.objectFields(reflect.TypeOf(ContractOuter{}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, field := range fields {
		names = append(names, field.Name)
	}
	if !reflect.DeepEqual(names, []string{"embedded", "own"}) {
		t.Errorf("fields = %v, want [embedded own]", names)
	}
}

// TestSchemaShapeChangeDetection is the deliberate shape-change test. It uses a
// synthetic manifest so removing a real contract field later cannot make this
// test fail for the wrong reason.
func TestSchemaShapeChangeDetection(t *testing.T) {
	base := syntheticManifest()

	t.Run("removed field is breaking", func(t *testing.T) {
		mutated := cloneManifest(t, base)
		demo := mutated.Types["Demo"]
		demo.Fields = withoutField(demo.Fields, "keep")
		mutated.Types["Demo"] = demo

		changes := classifySchemaChange(base, mutated)
		if !hasBreaking(changes) {
			t.Fatalf("removing a field was not detected as breaking: %v", changes)
		}
	})

	t.Run("removed enum value is breaking", func(t *testing.T) {
		mutated := cloneManifest(t, base)
		state := mutated.Types["State"]
		state.Enum = state.Enum[:len(state.Enum)-1]
		mutated.Types["State"] = state

		if !hasBreaking(classifySchemaChange(base, mutated)) {
			t.Fatal("removing an enum value was not detected as breaking")
		}
	})

	t.Run("added optional field is additive", func(t *testing.T) {
		mutated := cloneManifest(t, base)
		demo := mutated.Types["Demo"]
		demo.Fields = append(demo.Fields, schemaField{Name: "extra", Type: "string", Optional: true})
		mutated.Types["Demo"] = demo

		changes := classifySchemaChange(base, mutated)
		if hasBreaking(changes) {
			t.Fatalf("adding an optional field was classified as breaking: %v", changes)
		}
		if len(changes) == 0 {
			t.Fatal("adding an optional field was not reported at all")
		}
	})

	t.Run("added required field is breaking", func(t *testing.T) {
		mutated := cloneManifest(t, base)
		demo := mutated.Types["Demo"]
		demo.Fields = append(demo.Fields, schemaField{Name: "extra", Type: "string"})
		mutated.Types["Demo"] = demo

		if !hasBreaking(classifySchemaChange(base, mutated)) {
			t.Fatal("adding a required field was not detected as breaking")
		}
	})
}

// syntheticManifest is a minimal contract used only by the change classifier
// tests, independent of the real payload shapes.
func syntheticManifest() schemaManifest {
	version := SchemaVersion
	return schemaManifest{
		Format:        schemaFormat,
		SchemaVersion: version,
		Commands: []schemaCommand{
			{Name: "demo", Description: "synthetic", Root: "Demo", SchemaVersion: version},
		},
		Types: map[string]schemaType{
			"Demo": {
				Type: "object",
				Fields: []schemaField{
					{Name: "schema_version", Type: "integer", Const: &version},
					{Name: "keep", Type: "string"},
					{Name: "optional", Type: "string", Optional: true},
				},
			},
			"State": {Type: "string", Enum: []string{"a", "b"}},
		},
	}
}

func cloneManifest(t *testing.T, manifest schemaManifest) schemaManifest {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var clone schemaManifest
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func withoutField(fields []schemaField, name string) []schemaField {
	out := make([]schemaField, 0, len(fields))
	for _, field := range fields {
		if field.Name != name {
			out = append(out, field)
		}
	}
	return out
}

func hasBreaking(changes []schemaChange) bool {
	for _, change := range changes {
		if change.Breaking {
			return true
		}
	}
	return false
}

// classifySchemaChange compares two manifests. It is test-only: it explains a
// golden-test failure and is unit-tested above. The rule is the one SPEC.md
// states: added optional fields keep the version; removals, type changes,
// changed presence or nullability, and added required fields bump it.
func classifySchemaChange(old, new schemaManifest) []schemaChange {
	var changes []schemaChange
	add := func(breaking bool, format string, args ...any) {
		changes = append(changes, schemaChange{Breaking: breaking, Description: fmt.Sprintf(format, args...)})
	}

	oldCommands := commandMap(old.Commands)
	newCommands := commandMap(new.Commands)
	for _, name := range sortedKeys(oldCommands) {
		before := oldCommands[name]
		after, ok := newCommands[name]
		if !ok {
			add(true, "command %q was removed", name)
			continue
		}
		if before.Root != after.Root {
			add(true, "command %q root changed from %s to %s", name, before.Root, after.Root)
		}
	}
	for _, name := range sortedKeys(newCommands) {
		if _, ok := oldCommands[name]; !ok {
			add(false, "command %q was added", name)
		}
	}

	for _, name := range sortedKeys(old.Types) {
		before := old.Types[name]
		after, ok := new.Types[name]
		if !ok {
			add(true, "type %q was removed", name)
			continue
		}
		if before.Type != after.Type {
			add(true, "type %q changed type from %s to %s", name, before.Type, after.Type)
		}
		if before.Format != after.Format {
			add(true, "type %q changed format", name)
		}
		classifyEnum(&changes, name, before.Enum, after.Enum)
		classifyFields(&changes, name, before.Fields, after.Fields)
	}
	for _, name := range sortedKeys(new.Types) {
		if _, ok := old.Types[name]; !ok {
			add(false, "type %q was added", name)
		}
	}
	return changes
}

func classifyEnum(changes *[]schemaChange, typeName string, old, new []string) {
	oldValues := make(map[string]bool, len(old))
	newValues := make(map[string]bool, len(new))
	for _, value := range old {
		oldValues[value] = true
	}
	for _, value := range new {
		newValues[value] = true
	}
	for _, value := range old {
		if !newValues[value] {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("enum value %q was removed from %s", value, typeName)})
		}
	}
	for _, value := range new {
		if !oldValues[value] {
			*changes = append(*changes, schemaChange{Breaking: false, Description: fmt.Sprintf("enum value %q was added to %s", value, typeName)})
		}
	}
}

func classifyFields(changes *[]schemaChange, typeName string, old, new []schemaField) {
	oldFields := fieldMap(old)
	newFields := fieldMap(new)
	for _, name := range sortedKeys(oldFields) {
		before := oldFields[name]
		after, ok := newFields[name]
		if !ok {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s was removed", typeName, name)})
			continue
		}
		if before.Type != after.Type {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed type from %s to %s", typeName, name, before.Type, after.Type)})
		}
		if before.Ref != after.Ref {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed ref from %q to %q", typeName, name, before.Ref, after.Ref)})
		}
		if before.Format != after.Format {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed format", typeName, name)})
		}
		if before.Nullable != after.Nullable {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed nullability", typeName, name)})
		}
		if before.Optional != after.Optional {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed presence", typeName, name)})
		}
		if !reflect.DeepEqual(before.Items, after.Items) {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed array items", typeName, name)})
		}
		if !reflect.DeepEqual(before.Values, after.Values) {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed map values", typeName, name)})
		}
		if (before.Const == nil) != (after.Const == nil) || (before.Const != nil && *before.Const != *after.Const) {
			*changes = append(*changes, schemaChange{Breaking: true, Description: fmt.Sprintf("field %s.%s changed const", typeName, name)})
		}
		classifyEnum(changes, typeName+"."+name, before.Enum, after.Enum)
	}
	for _, name := range sortedKeys(newFields) {
		if _, ok := oldFields[name]; ok {
			continue
		}
		field := newFields[name]
		*changes = append(*changes, schemaChange{
			Breaking:    !field.Optional,
			Description: fmt.Sprintf("field %s.%s was added (optional=%t)", typeName, name, field.Optional),
		})
	}
}

func commandMap(commands []schemaCommand) map[string]schemaCommand {
	out := make(map[string]schemaCommand, len(commands))
	for _, command := range commands {
		out[command.Name] = command
	}
	return out
}

func fieldMap(fields []schemaField) map[string]schemaField {
	out := make(map[string]schemaField, len(fields))
	for _, field := range fields {
		out[field.Name] = field
	}
	return out
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validateCommand resolves a command's root type and validates an example.
func validateCommand(t *testing.T, manifest schemaManifest, command string, value any) {
	t.Helper()
	var root string
	for _, candidate := range manifest.Commands {
		if candidate.Name == command {
			root = candidate.Root
			break
		}
	}
	if root == "" {
		t.Fatalf("manifest has no command %q", command)
	}
	validateNamedType(t, manifest, root, value, command)
}

func validateNamedType(t *testing.T, manifest schemaManifest, name string, value any, path string) {
	t.Helper()
	definition, ok := manifest.Types[name]
	if !ok {
		t.Fatalf("%s: type %q is not in the manifest", path, name)
	}
	switch definition.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s: type %s wants an object, got %T", path, name, value)
		}
		validateFields(t, manifest, definition.Fields, object, path)
	case "string":
		text, ok := value.(string)
		if !ok {
			t.Fatalf("%s: type %s wants a string, got %T", path, name, value)
		}
		validateEnum(t, definition.Enum, text, path)
	default:
		validateField(t, manifest, schemaField{Type: definition.Type, Format: definition.Format}, value, path)
	}
}

func validateFields(t *testing.T, manifest schemaManifest, fields []schemaField, object map[string]any, path string) {
	t.Helper()
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field.Name] = true
		value, present := object[field.Name]
		if !present {
			if !field.Optional {
				t.Fatalf("%s: required field %q is missing", path, field.Name)
			}
			continue
		}
		validateField(t, manifest, field, value, path+"."+field.Name)
	}
	for name := range object {
		if !known[name] {
			t.Fatalf("%s: unknown field %q", path, name)
		}
	}
}

func validateField(t *testing.T, manifest schemaManifest, field schemaField, value any, path string) {
	t.Helper()
	if value == nil {
		if !field.Nullable {
			t.Fatalf("%s: null is not allowed", path)
		}
		return
	}
	if field.Ref != "" {
		validateNamedType(t, manifest, field.Ref, value, path)
	} else {
		switch field.Type {
		case "object":
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%s: wants an object, got %T", path, value)
			}
			if field.Values != nil {
				for key, item := range object {
					validateField(t, manifest, *field.Values, item, path+"."+key)
				}
			}
		case "array":
			items, ok := value.([]any)
			if !ok {
				t.Fatalf("%s: wants an array, got %T", path, value)
			}
			if field.Items == nil {
				t.Fatalf("%s: array has no item schema", path)
			}
			for i, item := range items {
				validateField(t, manifest, *field.Items, item, fmt.Sprintf("%s[%d]", path, i))
			}
		case "string":
			text, ok := value.(string)
			if !ok {
				t.Fatalf("%s: wants a string, got %T", path, value)
			}
			validateEnum(t, field.Enum, text, path)
		case "integer":
			number, ok := value.(float64)
			if !ok || number != math.Trunc(number) {
				t.Fatalf("%s: wants an integer, got %v", path, value)
			}
			if field.Const != nil && int(number) != *field.Const {
				t.Fatalf("%s: wants const %d, got %v", path, *field.Const, value)
			}
		case "number":
			if _, ok := value.(float64); !ok {
				t.Fatalf("%s: wants a number, got %T", path, value)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				t.Fatalf("%s: wants a boolean, got %T", path, value)
			}
		case "any":
			// Anything is valid.
		default:
			t.Fatalf("%s: unknown schema type %q", path, field.Type)
		}
	}
	if field.Const != nil {
		number, ok := value.(float64)
		if !ok || int(number) != *field.Const {
			t.Fatalf("%s: wants const %d, got %v", path, *field.Const, value)
		}
	}
}

func validateEnum(t *testing.T, values []string, value, path string) {
	t.Helper()
	if len(values) == 0 {
		return
	}
	for _, allowed := range values {
		if value == allowed {
			return
		}
	}
	t.Fatalf("%s: %q is not one of %v", path, value, values)
}
