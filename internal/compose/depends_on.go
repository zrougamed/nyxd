package compose

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// DependsOn lists dependency service names for ordering.
//
// Docker / Compose spec allows either:
//   - list: depends_on: [db, cache]
//   - map:  depends_on: { db: { condition: service_healthy } }
//
// Map keys are used for the same topological ordering as the list form.
// condition values are accepted for compatibility but not interpreted separately —
// nyxd already uses healthchecks + sequential start for readiness.
type DependsOn []string

// UnmarshalYAML implements yaml.Unmarshaler for both sequence and mapping forms.
func (d *DependsOn) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind == 0 {
		*d = nil
		return nil
	}
	switch value.Kind {
	case yaml.SequenceNode:
		var seq []string
		if err := value.Decode(&seq); err != nil {
			return fmt.Errorf("depends_on: %w", err)
		}
		*d = DependsOn(seq)
		return nil
	case yaml.MappingNode:
		var m map[string]any
		if err := value.Decode(&m); err != nil {
			return fmt.Errorf("depends_on: %w", err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		*d = DependsOn(keys)
		return nil
	case yaml.ScalarNode:
		if value.Tag == "!!null" || value.Value == "" || value.Value == "null" {
			*d = nil
			return nil
		}
		return fmt.Errorf("depends_on: unexpected scalar %q", value.Value)
	default:
		return fmt.Errorf("depends_on: expected sequence or mapping, got yaml kind %d", value.Kind)
	}
}
