package schema

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	messageRe       = regexp.MustCompile(`^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$`)
	fieldRe         = regexp.MustCompile(`^(?:(repeated)\s+)?([A-Za-z_][A-Za-z0-9_\.]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`)
	mapRe           = regexp.MustCompile(`^map<\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*,\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*>\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`)
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

		if current == nil {
			matches := messageRe.FindStringSubmatch(line)
			if matches == nil {
				continue
			}

			snapshot.Messages = append(snapshot.Messages, Message{Name: matches[1]})
			current = &snapshot.Messages[len(snapshot.Messages)-1]

			continue
		}

		if line == "}" {
			current = nil
			continue
		}

		if matches := reservedRe.FindStringSubmatch(line); matches != nil {
			err := parseReserved(current, matches[1])
			if err != nil {
				return Snapshot{}, fmt.Errorf("line %d: %w", lineno+1, err)
			}

			continue
		}

		if matches := mapRe.FindStringSubmatch(line); matches != nil {
			number, err := strconv.Atoi(matches[4])
			if err != nil {
				return Snapshot{}, fmt.Errorf("line %d: parse field number %q: %w", lineno+1, matches[4], err)
			}

			current.Fields = append(current.Fields, Field{
				Number: number,
				Name:   matches[3],
				Type:   "map",
				Key:    matches[1],
				Value:  matches[2],
			})

			continue
		}

		if matches := fieldRe.FindStringSubmatch(line); matches != nil {
			number, err := strconv.Atoi(matches[4])
			if err != nil {
				return Snapshot{}, fmt.Errorf("line %d: parse field number %q: %w", lineno+1, matches[4], err)
			}

			current.Fields = append(current.Fields, Field{
				Number: number,
				Name:   matches[3],
				Type:   matches[2],
				Label:  matches[1],
			})
		}
	}

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

	return snapshot, nil
}

func parseReserved(message *Message, spec string) error {
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if matches := reservedNameRe.FindStringSubmatch(part); matches != nil {
			message.ReservedNames = append(message.ReservedNames, matches[1])
			continue
		}

		matches := reservedRangeRe.FindStringSubmatch(part)
		if matches == nil {
			return fmt.Errorf("unsupported reserved entry %q in message %s", part, message.Name)
		}

		lo, err := strconv.Atoi(matches[1])
		if err != nil {
			return err
		}

		hi := lo
		if matches[2] != "" {
			if hi, err = strconv.Atoi(matches[2]); err != nil {
				return err
			}
		}

		if hi < lo {
			return fmt.Errorf("invalid reserved range %q in message %s", part, message.Name)
		}

		for n := lo; n <= hi; n++ {
			message.ReservedNumbers = append(message.ReservedNumbers, n)
		}
	}

	return nil
}

func stripLineComment(line string) string {
	if before, _, ok := strings.Cut(line, "//"); ok {
		return before
	}

	return line
}
