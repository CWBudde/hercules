package schema

import (
	"fmt"
	"sort"
)

// Change is one classified difference between two schema snapshots.
type Change struct {
	Breaking    bool
	Description string
}

// Result is the outcome of Evaluate: the classified changes plus any policy
// violations that must fail CI.
type Result struct {
	Changes  []Change
	Breaking bool
	Errors   []string
}

// Compare classifies the differences between two snapshots according to the
// PB schema change policy in docs/SCHEMAS.md. Snapshot.Version is not
// compared here; Evaluate enforces the version-bump rule.
func Compare(old, updated Snapshot) []Change {
	var changes []Change
	add := func(breaking bool, format string, args ...any) {
		changes = append(changes, Change{Breaking: breaking, Description: fmt.Sprintf(format, args...)})
	}

	oldMessages := messageIndex(old)
	newMessages := messageIndex(updated)

	for _, name := range sortedKeys(oldMessages) {
		if _, ok := newMessages[name]; !ok {
			add(true, "message %s removed", name)
		}
	}

	for _, name := range sortedKeys(newMessages) {
		if _, ok := oldMessages[name]; !ok {
			add(false, "message %s added", name)
		}
	}

	for _, name := range sortedKeys(oldMessages) {
		newMessage, ok := newMessages[name]
		if !ok {
			continue
		}

		compareMessage(oldMessages[name], newMessage, add)
	}

	return changes
}

func compareMessage(old, updated Message, add func(bool, string, ...any)) {
	oldFields := fieldIndex(old)
	newFields := fieldIndex(updated)
	oldReservedNumbers := intSet(old.ReservedNumbers)
	newReservedNumbers := intSet(updated.ReservedNumbers)
	oldReservedNames := stringSet(old.ReservedNames)
	newReservedNames := stringSet(updated.ReservedNames)

	compareExistingFields(old, oldFields, newFields, newReservedNumbers, newReservedNames, add)
	compareAddedFields(updated, oldFields, newFields, oldReservedNumbers, oldReservedNames, add)
	compareReservedNumbers(old, updated, oldReservedNumbers, newReservedNumbers, add)
	compareReservedNames(old, updated, oldReservedNames, newReservedNames, add)
}

func compareExistingFields(
	message Message,
	oldFields, newFields map[int]Field,
	newReservedNumbers map[int]bool,
	newReservedNames map[string]bool,
	add func(bool, string, ...any),
) {
	for _, number := range sortedIntKeys(oldFields) {
		oldField := oldFields[number]

		newField, ok := newFields[number]
		if !ok {
			if newReservedNumbers[number] && newReservedNames[oldField.Name] {
				add(true, "field %s.%s (%d) removed", message.Name, oldField.Name, number)
			} else {
				add(true, "field %s.%s (%d) removed without reserving its number and name",
					message.Name, oldField.Name, number)
			}

			continue
		}

		if oldField.Name != newField.Name {
			add(true, "field %s.%s (%d) renamed to %s", message.Name, oldField.Name, number, newField.Name)
			continue
		}

		if oldField.Type != newField.Type || oldField.Key != newField.Key || oldField.Value != newField.Value {
			add(true, "field %s.%s (%d) changed type from %s to %s",
				message.Name, oldField.Name, number, fieldType(oldField), fieldType(newField))
		}

		if oldField.Label != newField.Label {
			add(true, "field %s.%s (%d) changed label from %q to %q",
				message.Name, oldField.Name, number, oldField.Label, newField.Label)
		}
	}
}

func compareAddedFields(
	message Message,
	oldFields, newFields map[int]Field,
	oldReservedNumbers map[int]bool,
	oldReservedNames map[string]bool,
	add func(bool, string, ...any),
) {
	for _, number := range sortedIntKeys(newFields) {
		newField := newFields[number]
		if _, ok := oldFields[number]; ok {
			continue
		}

		switch {
		case oldReservedNumbers[number]:
			add(true, "field %s.%s (%d) reuses reserved number %d", message.Name, newField.Name, number, number)
		case oldReservedNames[newField.Name]:
			add(true, "field %s.%s (%d) reuses reserved name %q", message.Name, newField.Name, number, newField.Name)
		default:
			add(false, "field %s.%s (%d) added", message.Name, newField.Name, number)
		}
	}
}

func compareReservedNumbers(
	old, updated Message,
	oldReservedNumbers, newReservedNumbers map[int]bool,
	add func(bool, string, ...any),
) {
	for _, number := range sortedIntSet(oldReservedNumbers) {
		if !newReservedNumbers[number] {
			add(true, "message %s un-reserved number %d", old.Name, number)
		}
	}

	for _, number := range sortedIntSet(newReservedNumbers) {
		if !oldReservedNumbers[number] {
			add(false, "message %s: reserved number %d added", updated.Name, number)
		}
	}
}

func compareReservedNames(
	old, updated Message,
	oldReservedNames, newReservedNames map[string]bool,
	add func(bool, string, ...any),
) {
	for _, name := range sortedStringSet(oldReservedNames) {
		if !newReservedNames[name] {
			add(true, "message %s un-reserved name %q", old.Name, name)
		}
	}

	for _, name := range sortedStringSet(newReservedNames) {
		if !oldReservedNames[name] {
			add(false, "message %s: reserved name %q added", updated.Name, name)
		}
	}
}

// Evaluate applies the CI policy: any schema change requires a changelog
// entry, and breaking changes additionally require a schema version bump.
func Evaluate(old, updated Snapshot, changelogUpdated bool) Result {
	result := Result{Changes: Compare(old, updated)}
	for _, change := range result.Changes {
		if change.Breaking {
			result.Breaking = true
			break
		}
	}

	if len(result.Changes) > 0 && !changelogUpdated {
		result.Errors = append(result.Errors,
			"the PB schema changed but docs/SCHEMA_CHANGELOG.md was not updated in the same change")
	}

	if result.Breaking && updated.Version <= old.Version {
		result.Errors = append(result.Errors, fmt.Sprintf(
			"breaking PB schema change requires a version bump: pb.SchemaVersion is still %d (was %d)",
			updated.Version, old.Version,
		))
	}

	return result
}

func fieldType(field Field) string {
	if field.Type == "map" {
		return fmt.Sprintf("map<%s, %s>", field.Key, field.Value)
	}

	return field.Type
}

func messageIndex(snapshot Snapshot) map[string]Message {
	index := make(map[string]Message, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		index[message.Name] = message
	}

	return index
}

func fieldIndex(message Message) map[int]Field {
	index := make(map[int]Field, len(message.Fields))
	for _, field := range message.Fields {
		index[field.Number] = field
	}

	return index
}

func intSet(values []int) map[int]bool {
	set := make(map[int]bool, len(values))
	for _, v := range values {
		set[v] = true
	}

	return set
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}

	return set
}

func sortedKeys(m map[string]Message) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func sortedIntKeys(m map[int]Field) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	return keys
}

func sortedIntSet(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	return keys
}

func sortedStringSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
