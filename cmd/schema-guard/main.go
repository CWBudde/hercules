// schema-guard compares two PB schema snapshots (internal/pb/pb.schema.json)
// and enforces the compatibility policy from docs/SCHEMAS.md: every schema
// change needs a docs/SCHEMA_CHANGELOG.md entry, and breaking changes
// additionally need a pb.SchemaVersion bump. CI runs it against the merge
// base (see .github/workflows/test-schema.yaml); locally use
// `just check-schema [base]`.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cwbudde/hercules/internal/pb/schema"
)

func main() {
	oldPath := flag.String("old", "", "baseline snapshot JSON (e.g. pb.schema.json from the merge base)")
	newPath := flag.String("new", "internal/pb/pb.schema.json", "current snapshot JSON")
	changelogUpdated := flag.Bool("changelog-updated", false,
		"set when docs/SCHEMA_CHANGELOG.md was updated in the same change")
	flag.Parse()

	if *oldPath == "" {
		fmt.Fprintln(os.Stderr, "schema-guard: -old is required")
		flag.Usage()
		os.Exit(2)
	}

	oldSnapshot, err := schema.Load(*oldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schema-guard: load baseline: %v\n", err)
		os.Exit(2)
	}
	newSnapshot, err := schema.Load(*newPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schema-guard: load current: %v\n", err)
		os.Exit(2)
	}

	result := schema.Evaluate(oldSnapshot, newSnapshot, *changelogUpdated)

	if len(result.Changes) == 0 {
		fmt.Fprintln(os.Stdout, "schema-guard: no PB schema changes")
		return
	}

	fmt.Fprintf(os.Stdout, "schema-guard: %d PB schema change(s) vs baseline (schema version %d -> %d):\n",
		len(result.Changes), oldSnapshot.Version, newSnapshot.Version)
	for _, change := range result.Changes {
		kind := "compatible"
		if change.Breaking {
			kind = "BREAKING"
		}
		fmt.Fprintf(os.Stdout, "  [%-10s] %s\n", kind, change.Description)
	}

	if len(result.Errors) > 0 {
		fmt.Fprintln(os.Stdout, "\nschema-guard: policy violations (see docs/SCHEMAS.md \"PB Schema Change Policy\"):")
		for _, msg := range result.Errors {
			fmt.Fprintf(os.Stdout, "  - %s\n", msg)
		}
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "schema-guard: OK (changes are recorded per policy)")
}
