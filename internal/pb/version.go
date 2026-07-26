package pb

// SchemaVersion is the version of the Hercules output schema (both the
// protobuf messages in pb.proto and the documented YAML structure). It is
// emitted as Metadata.version in --pb output and as `hercules.version` in the
// YAML header, and is snapshotted in pb.schema.json.
//
// Bump it for every breaking schema change as defined in
// docs/SCHEMAS.md ("PB Schema Change Policy") and record the change in
// docs/SCHEMA_CHANGELOG.md. Compatible additions do not bump it.
const (
	// SchemaVersion is the schema emitted by current Hercules binaries.
	SchemaVersion = 2

	// OldestSupportedSchemaVersion is the oldest protobuf envelope which has
	// an explicit migration to SchemaVersion. Older protobuf payloads must not
	// be decoded as the current schema.
	OldestSupportedSchemaVersion int32 = 1

	// LegacyYAMLSchemaVersion is the value emitted by affected Hercules builds
	// after the module rename. Those files already use the schema-2 YAML shape
	// and can be normalized after their complete contents have been validated.
	LegacyYAMLSchemaVersion int32 = 0
)

// IsSupportedSchemaVersion reports whether a protobuf schema has a complete
// migration path to the current schema.
func IsSupportedSchemaVersion(version int32) bool {
	switch version {
	case OldestSupportedSchemaVersion, SchemaVersion:
		return true
	default:
		return false
	}
}
