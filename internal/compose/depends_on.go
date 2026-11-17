package compose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zrougamed/nyxd/internal/health"
)

// depends_on conditions from the Compose spec.
const (
	ConditionStarted   = "service_started"
	ConditionHealthy   = "service_healthy"
	ConditionCompleted = "service_completed_successfully"
)

// Dependency is one depends_on entry: the service and when it counts as ready.
type Dependency struct {
	Service string
	// Condition is one of the Condition* constants; the list form and an
	// omitted condition mean ConditionStarted.
	Condition string
	// Restart is the long form's restart: true (not supported yet: warned).
	Restart bool
}

// DependsOn lists a service's dependencies, sorted by service name.
//
// Docker / Compose spec allows either:
//   - list: depends_on: [db, cache]
//   - map:  depends_on: { db: { condition: service_healthy } }
//
// Both forms order the start (dependencies first); compose up then waits for
// each dependency's condition before starting the services that need it.
type DependsOn []Dependency

// Names returns the dependency service names.
func (d DependsOn) Names() []string {
	out := make([]string, len(d))
	for i, dep := range d {
		out[i] = dep.Service
	}
	return out
}

type dependencyYAML struct {
	Condition string `yaml:"condition"`
	Restart   bool   `yaml:"restart"`
	Required  *bool  `yaml:"required"`
}

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
		out := make(DependsOn, len(seq))
		for i, name := range seq {
			out[i] = Dependency{Service: name, Condition: ConditionStarted}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
		*d = out
		return nil
	case yaml.MappingNode:
		var m map[string]*dependencyYAML
		if err := value.Decode(&m); err != nil {
			return fmt.Errorf("depends_on: %w", err)
		}
		out := make(DependsOn, 0, len(m))
		for name, v := range m {
			dep := Dependency{Service: name, Condition: ConditionStarted}
			if v != nil {
				if c := strings.TrimSpace(v.Condition); c != "" {
					dep.Condition = c
				}
				dep.Restart = v.Restart
			}
			switch dep.Condition {
			case ConditionStarted, ConditionHealthy, ConditionCompleted:
			default:
				return fmt.Errorf("depends_on %q: unknown condition %q (supported: %s, %s, %s)",
					name, dep.Condition, ConditionStarted, ConditionHealthy, ConditionCompleted)
			}
			out = append(out, dep)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
		*d = out
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

// validateDependsOn checks that every condition can be met: service_healthy
// needs a health check on the dependency, otherwise compose up would wait for
// a status that never comes.
func validateDependsOn(name string, svc *Service, all map[string]Service) error {
	for _, dep := range svc.DependsOn {
		target, ok := all[dep.Service]
		if !ok {
			return fmt.Errorf("service %q: unknown depends_on %q", name, dep.Service)
		}
		if dep.Condition == ConditionHealthy && !hasHealthcheck(target.Healthcheck) {
			return fmt.Errorf("service %q: depends_on %q with condition %s, but %q has no healthcheck in the compose file",
				name, dep.Service, ConditionHealthy, dep.Service)
		}
	}
	return nil
}

func hasHealthcheck(h *Healthcheck) bool {
	cfg, err := HealthcheckToConfig(h)
	return err == nil && cfg != nil && cfg.Type != health.TypeNone
}

// DependsOnWarnings lists depends_on settings compose up accepts but does not
// act on, and conditions that cannot be met as configured.
func DependsOnWarnings(stack *Stack) []string {
	if stack == nil {
		return nil
	}
	names := make([]string, 0, len(stack.Services))
	for n := range stack.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		for _, dep := range stack.Services[n].DependsOn {
			if dep.Restart {
				out = append(out, fmt.Sprintf("service %q: depends_on %q restart: true is not supported yet (ignored)", n, dep.Service))
			}
			if dep.Condition != ConditionCompleted {
				continue
			}
			switch strings.ToLower(string(stack.Services[dep.Service].Restart)) {
			case string(RestartAlways), string(RestartUnlessStopped):
				out = append(out, fmt.Sprintf("service %q: depends_on %q with condition %s, but %q restarts %s and never completes",
					n, dep.Service, ConditionCompleted, dep.Service, stack.Services[dep.Service].Restart))
			}
		}
	}
	return out
}
