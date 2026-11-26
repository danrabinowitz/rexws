// Package contract holds the version of the rexws --json contract. The CLI,
// the operations layer, and the golden tests all read the one constant here so
// they cannot disagree about the version.
package contract

// SchemaVersion is the version of every --json payload. Additive fields keep
// the version; breaking changes bump it. See docs/json-schema.md and SPEC.md.
const SchemaVersion = 1
