package pb

// SchemaVersion is the version of the Hercules output schema (both the
// protobuf messages in pb.proto and the documented YAML structure). It is
// emitted as Metadata.version in --pb output and as `hercules.version` in the
// YAML header, and is snapshotted in pb.schema.json.
//
// Bump it for every breaking schema change as defined in
// docs/SCHEMAS.md ("PB Schema Change Policy") and record the change in
// docs/SCHEMA_CHANGELOG.md. Compatible additions do not bump it.
const SchemaVersion = 2
