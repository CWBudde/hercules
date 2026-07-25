package schema

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	errUnsupportedReservedEntry = errors.New("unsupported reserved entry")
	errInvalidReservedRange     = errors.New("invalid reserved range")
)

const (
	fieldTypeMap    = "map"
	fieldTypeString = "string"
)

var (
	messageRe = regexp.MustCompile(`^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$`)
	fieldRe   = regexp.MustCompile(
		`^(?:(repeated)\s+)?([A-Za-z_][A-Za-z0-9_\.]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`,
	)
	mapRe = regexp.MustCompile(
		`^map<\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*,\s*` +
			`([A-Za-z_][A-Za-z0-9_\.]*)\s*>\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`,
	)
	reservedRe      = regexp.MustCompile(`^reserved\s+(.+?)\s*;`)
	reservedRangeRe = regexp.MustCompile(`^([0-9]+)(?:\s+to\s+([0-9]+))?$`)
	reservedNameRe  = regexp.MustCompile(`^"([A-Za-z_][A-Za-z0-9_]*)"$`)
)

// ParseProto extracts messages, fields, and reserved statements from a
// .proto file. It understands the subset of proto3 used by pb.proto
// (top-level messages, scalar/message/repeated/map fields, reserved
// numbers/ranges/names) and returns the result sorted by message name and
// field number. Snapshot.Version is left zero; the caller stamps it.
func ParseProto(data []byte) (Snapshot, error) {
	var snapshot Snapshot
	var current *Message

	for lineno, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripLineComment(raw))
		if line == "" {
			continue
		}

		var err error

		current, err = parseProtoLine(&snapshot, current, line)
		if err != nil {
			return Snapshot{}, fmt.Errorf("line %d: %w", lineno+1, err)
		}
	}

	sortSnapshot(&snapshot)

	return snapshot, nil
}

func parseProtoLine(snapshot *Snapshot, current *Message, line string) (*Message, error) {
	if current == nil {
		matches := messageRe.FindStringSubmatch(line)
		if matches == nil {
			return nil, nil //nolint:nilnil // No message is open and this line is intentionally ignored.
		}

		snapshot.Messages = append(snapshot.Messages, Message{Name: matches[1]})

		return &snapshot.Messages[len(snapshot.Messages)-1], nil
	}

	if line == "}" {
		return nil, nil //nolint:nilnil // Closing a message intentionally clears the current message.
	}

	if matches := reservedRe.FindStringSubmatch(line); matches != nil {
		return current, parseReserved(current, matches[1])
	}

	if matches := mapRe.FindStringSubmatch(line); matches != nil {
		number, err := parseFieldNumber(matches[4])
		if err != nil {
			return current, err
		}

		current.Fields = append(current.Fields, Field{
			Number: number, Name: matches[3], Type: fieldTypeMap, Key: matches[1], Value: matches[2],
		})

		return current, nil
	}

	if matches := fieldRe.FindStringSubmatch(line); matches != nil {
		number, err := parseFieldNumber(matches[4])
		if err != nil {
			return current, err
		}

		current.Fields = append(current.Fields, Field{
			Number: number, Name: matches[3], Type: matches[2], Label: matches[1],
		})
	}

	return current, nil
}

func parseFieldNumber(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse field number %q: %w", raw, err)
	}

	return number, nil
}

func sortSnapshot(snapshot *Snapshot) {
	sort.Slice(snapshot.Messages, func(i, j int) bool {
		return snapshot.Messages[i].Name < snapshot.Messages[j].Name
	})

	for i := range snapshot.Messages {
		message := &snapshot.Messages[i]
		sort.Slice(message.Fields, func(j, k int) bool {
			return message.Fields[j].Number < message.Fields[k].Number
		})
		sort.Ints(message.ReservedNumbers)
		sort.Strings(message.ReservedNames)
	}
}

func parseReserved(message *Message, spec string) error {
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)

		err := parseReservedPart(message, part)
		if err != nil {
			return err
		}
	}

	return nil
}

func parseReservedPart(message *Message, part string) error {
	if matches := reservedNameRe.FindStringSubmatch(part); matches != nil {
		message.ReservedNames = append(message.ReservedNames, matches[1])

		return nil
	}

	rangeStart, rangeEnd, err := parseReservedRange(part, message.Name)
	if err != nil {
		return err
	}

	for n := rangeStart; n <= rangeEnd; n++ {
		message.ReservedNumbers = append(message.ReservedNumbers, n)
	}

	return nil
}

func parseReservedRange(part, messageName string) (int, int, error) {
	matches := reservedRangeRe.FindStringSubmatch(part)
	if matches == nil {
		return 0, 0, fmt.Errorf("%w %q in message %s", errUnsupportedReservedEntry, part, messageName)
	}

	rangeStart, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse reserved range start %q: %w", matches[1], err)
	}

	rangeEnd, err := parseReservedRangeEnd(matches[2], rangeStart)
	if err != nil {
		return 0, 0, err
	}

	if rangeEnd < rangeStart {
		return 0, 0, fmt.Errorf("%w %q in message %s", errInvalidReservedRange, part, messageName)
	}

	return rangeStart, rangeEnd, nil
}

func parseReservedRangeEnd(raw string, rangeStart int) (int, error) {
	if raw == "" {
		return rangeStart, nil
	}

	rangeEnd, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse reserved range end %q: %w", raw, err)
	}

	return rangeEnd, nil
}

func stripLineComment(line string) string {
	if before, _, ok := strings.Cut(line, "//"); ok {
		return before
	}

	return line
}
