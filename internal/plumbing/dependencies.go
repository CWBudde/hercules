package plumbing

import (
	"errors"
	"fmt"
)

var errInvalidDependencyType = errors.New("invalid dependency type")

func dependencyValue[T any](dependencies map[string]any, name string) (T, error) {
	var zero T

	value, exists := dependencies[name]
	if !exists {
		return zero, fmt.Errorf("%w: %q is missing", errInvalidDependencyType, name)
	}

	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf(
			"%w for %q: got %T, expected %T",
			errInvalidDependencyType,
			name,
			value,
			zero,
		)
	}

	return typed, nil
}
