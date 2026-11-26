package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/agentstate"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// schemaFormat names the representation `rexws schema` emits. It is a field
// manifest, not JSON Schema: each type is described by a primitive type, an
// optional reference to another named type, array items, map values, an enum,
// and the nullable/optional/const flags. docs/json-schema.md documents every
// command and type.
const schemaFormat = "rexws-field-manifest"

// schemaManifest is the machine-readable description of the whole --json
// contract. It is static: building it reads no config, starts no Rex process,
// and inspects no repository.
type schemaManifest struct {
	Format        string                `json:"format"`
	SchemaVersion int                   `json:"schema_version"`
	Commands      []schemaCommand       `json:"commands"`
	Types         map[string]schemaType `json:"types"`
}

// schemaCommand names one --json document and points at its root type.
type schemaCommand struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Root          string `json:"root"`
	SchemaVersion int    `json:"schema_version"`
}

// schemaType describes one named JSON object or enum.
type schemaType struct {
	Type   string        `json:"type"`
	Format string        `json:"format,omitempty"`
	Fields []schemaField `json:"fields,omitempty"`
	Enum   []string      `json:"enum,omitempty"`
}

// schemaField describes one field of an object. A Ref names a type in the
// manifest's types map; Items describes an array element and Values a map
// value. Optional is true when the field carries json:",omitempty"; Nullable
// is true only when the field can be JSON null. Const fixes a field to a single
// value.
type schemaField struct {
	Name     string       `json:"name,omitempty"`
	Type     string       `json:"type"`
	Ref      string       `json:"ref,omitempty"`
	Format   string       `json:"format,omitempty"`
	Items    *schemaField `json:"items,omitempty"`
	Values   *schemaField `json:"values,omitempty"`
	Enum     []string     `json:"enum,omitempty"`
	Const    *int         `json:"const,omitempty"`
	Nullable bool         `json:"nullable,omitempty"`
	Optional bool         `json:"optional,omitempty"`
}

// contractCommand is the builder-side description of a command's root type.
type contractCommand struct {
	name        string
	description string
	root        reflect.Type
}

// contractCommands is the fixed command order in the manifest. Add a command
// here when it gains a --json document; TestContractCoversEveryJSONCommand
// fails if a --json Cobra command is missing.
func contractCommands() []contractCommand {
	return []contractCommand{
		{
			name:        "list",
			description: "Discovered workstreams merged with workflow and Rex state.",
			root:        reflect.TypeOf(ListPayload{}),
		},
		{
			name:        "status",
			description: "The workstream path and workflow stage recorded by status.",
			root:        reflect.TypeOf(StatusPayload{}),
		},
		{
			name:        "switch",
			description: "The Rex session and optional window selected by switch.",
			root:        reflect.TypeOf(SwitchPayload{}),
		},
		{
			name:        "new",
			description: "The worktree created by new, plus the Rex session when --switch was used.",
			root:        reflect.TypeOf(NewPayload{}),
		},
		{
			name:        "rm",
			description: "The removal result, including retained branches, loss, and partial-failure reason.",
			root:        reflect.TypeOf(RmPayload{}),
		},
		{
			name:        "project add",
			description: "The project registration made by project add.",
			root:        reflect.TypeOf(ProjectAddPayload{}),
		},
		{
			name:        "here",
			description: "The workstream containing the current directory.",
			root:        reflect.TypeOf(HerePayload{}),
		},
		{
			name:        "cleanup",
			description: "The preview-first cleanup report with worktree, branch, and orphan classifications.",
			root:        reflect.TypeOf(ops.CleanupReport{}),
		},
	}
}

// buildSchemaManifest derives the manifest from the payload types by
// reflection. The result is deterministic: commands keep their declared order,
// object fields keep Go struct order, and the types map is sorted by
// encoding/json.
func buildSchemaManifest() (schemaManifest, error) {
	commands := contractCommands()
	builder := &manifestBuilder{
		types: make(map[string]schemaType),
		seen:  make(map[reflect.Type]bool),
		names: make(map[string]reflect.Type),
	}
	out := make([]schemaCommand, 0, len(commands))
	for _, contract := range commands {
		if err := builder.add(contract.root); err != nil {
			return schemaManifest{}, err
		}
		out = append(out, schemaCommand{
			Name:          contract.name,
			Description:   contract.description,
			Root:          contract.root.Name(),
			SchemaVersion: SchemaVersion,
		})
	}
	return schemaManifest{
		Format:        schemaFormat,
		SchemaVersion: SchemaVersion,
		Commands:      out,
		Types:         builder.types,
	}, nil
}

type manifestBuilder struct {
	types map[string]schemaType
	seen  map[reflect.Type]bool
	names map[string]reflect.Type
}

// add registers a named type and every named type reachable from it.
func (b *manifestBuilder) add(t reflect.Type) error {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() == "" {
		return nil
	}
	if t == reflect.TypeOf(time.Time{}) {
		return nil
	}
	if b.seen[t] {
		return nil
	}
	if existing, ok := b.names[t.Name()]; ok && existing != t {
		return fmt.Errorf("schema: duplicate type name %q for %s and %s", t.Name(), existing, t)
	}
	b.seen[t] = true
	b.names[t.Name()] = t

	switch t.Kind() {
	case reflect.Struct:
		fields, err := b.objectFields(t)
		if err != nil {
			return err
		}
		b.types[t.Name()] = schemaType{Type: "object", Fields: fields}
	case reflect.String:
		b.types[t.Name()] = schemaType{Type: "string", Enum: enumValues(t)}
	default:
		b.types[t.Name()] = schemaType{Type: primitiveType(t)}
	}
	return nil
}

// objectFields returns the JSON fields of a struct. Embedded structs without
// a JSON name are flattened, matching encoding/json.
func (b *manifestBuilder) objectFields(t reflect.Type) ([]schemaField, error) {
	fields := make([]schemaField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		if field.Anonymous {
			name, _, _ := parseJSONTag(tag, "")
			if name == "" {
				embedded := field.Type
				for embedded.Kind() == reflect.Ptr {
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					nested, err := b.objectFields(embedded)
					if err != nil {
						return nil, err
					}
					fields = append(fields, nested...)
					continue
				}
			}
		}
		name, optional, ok := parseJSONTag(tag, field.Name)
		if !ok {
			continue
		}
		schema, err := b.field(t.Name(), field.Type, name, optional)
		if err != nil {
			return nil, err
		}
		fields = append(fields, schema)
	}
	return fields, nil
}

// field describes one struct field. A pointer with omitempty is optional but
// not nullable: a nil value is omitted, never serialized as null.
func (b *manifestBuilder) field(parent string, t reflect.Type, name string, optional bool) (schemaField, error) {
	pointer := t.Kind() == reflect.Ptr
	schema, err := b.value(t)
	if err != nil {
		return schemaField{}, err
	}
	schema.Name = name
	if pointer && optional {
		schema.Nullable = false
	}
	schema.Optional = optional
	if values := fieldEnum(parent, name); len(values) > 0 {
		schema.Enum = values
	}
	if name == "schema_version" {
		version := SchemaVersion
		schema.Type = "integer"
		schema.Ref = ""
		schema.Format = ""
		schema.Enum = nil
		schema.Const = &version
	}
	return schema, nil
}

// value describes a type without a field name.
func (b *manifestBuilder) value(t reflect.Type) (schemaField, error) {
	var schema schemaField
	for t.Kind() == reflect.Ptr {
		schema.Nullable = true
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		schema.Type = "string"
		schema.Format = "date-time"
		return schema, nil
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		schema.Type = "array"
		item, err := b.value(t.Elem())
		if err != nil {
			return schemaField{}, err
		}
		schema.Items = &item
	case reflect.Map:
		schema.Type = "object"
		value, err := b.value(t.Elem())
		if err != nil {
			return schemaField{}, err
		}
		schema.Values = &value
	case reflect.Struct:
		schema.Type = "object"
		if t.Name() != "" {
			schema.Ref = t.Name()
			if err := b.add(t); err != nil {
				return schemaField{}, err
			}
		}
	case reflect.String:
		schema.Type = "string"
		if t.PkgPath() != "" {
			schema.Ref = t.Name()
			if err := b.add(t); err != nil {
				return schemaField{}, err
			}
		}
	default:
		schema.Type = primitiveType(t)
	}
	return schema, nil
}

func primitiveType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	default:
		return "any"
	}
}

// parseJSONTag splits a json struct tag. A "-" tag hides the field. An absent
// or unnamed tag uses the Go field name.
func parseJSONTag(tag, fallback string) (name string, optional, ok bool) {
	if tag == "-" {
		return "", false, false
	}
	if tag == "" {
		return fallback, false, true
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, false
	}
	name = parts[0]
	if name == "" {
		name = fallback
	}
	for _, option := range parts[1:] {
		if option == "omitempty" {
			optional = true
		}
	}
	return name, optional, true
}

// fieldEnum returns the value set of a plain string field whose values are not
// a named type. Named enums live on their type definition instead.
func fieldEnum(parent, name string) []string {
	if parent == "CleanupItem" && name == "kind" {
		return ops.CleanupKinds()
	}
	return nil
}

// enumValues returns the canonical value set of a named string type.
// config.Status is intentionally absent: its values come from config.json.
func enumValues(t reflect.Type) []string {
	switch t {
	case reflect.TypeOf(agentstate.State("")):
		return stringValues(agentstate.Values())
	case reflect.TypeOf(ops.CleanupClassification("")):
		return stringValues(ops.CleanupClassifications())
	case reflect.TypeOf(ops.CleanupBranchState("")):
		return stringValues(ops.CleanupBranchStates())
	case reflect.TypeOf(ops.CleanupAction("")):
		return stringValues(ops.CleanupActions())
	case reflect.TypeOf(ops.CleanupResult("")):
		return stringValues(ops.CleanupResults())
	case reflect.TypeOf(ops.CleanupOrphanKind("")):
		return stringValues(ops.CleanupOrphanKinds())
	default:
		return nil
	}
}

func stringValues[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the machine-readable JSON contract",
		Long: "Print the field manifest for every --json command. The command needs no\n" +
			"configuration, Rex, or repository: it emits the static contract to stdout\n" +
			"as one JSON document. docs/json-schema.md documents the same shapes in prose.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := buildSchemaManifest()
			if err != nil {
				return err
			}
			return encodeJSON(cmd, manifest)
		},
	}
}
