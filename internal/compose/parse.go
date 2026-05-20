// Package compose parses a minimal nyx-compose.yaml into a typed Stack.
package compose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse decodes compose YAML from bytes and validates the stack.
func Parse(data []byte) (*Stack, error) {
	var st Stack
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("compose yaml: %w", err)
	}

	for name, svc := range st.Services {
		if err := validateService(name, &svc, st.Services); err != nil {
			return nil, err
		}
		st.Services[name] = svc
	}

	if err := detectCycles(st.Services); err != nil {
		return nil, err
	}

	return &st, nil
}

func validateService(name string, svc *Service, all map[string]Service) error {
	if strings.TrimSpace(svc.Image) == "" {
		return fmt.Errorf("service %q: missing image", name)
	}
	if err := validateRestart(svc.Restart); err != nil {
		return fmt.Errorf("service %q: %w", name, err)
	}
	for _, dep := range svc.DependsOn {
		if _, ok := all[dep]; !ok {
			return fmt.Errorf("service %q: unknown depends_on %q", name, dep)
		}
	}
	if svc.NoNewPrivileges == nil {
		t := true
		svc.NoNewPrivileges = &t
	}
	return nil
}

func validateRestart(r RestartPolicy) error {
	switch strings.ToLower(string(r)) {
	case "", string(RestartNo), string(RestartAlways), string(RestartOnFailure), string(RestartUnlessStopped):
		return nil
	default:
		return fmt.Errorf("invalid restart policy %q", r)
	}
}

func detectCycles(services map[string]Service) error {
	color := make(map[string]int8)

	var dfs func(string) error
	dfs = func(n string) error {
		switch color[n] {
		case 1:
			return fmt.Errorf("dependency cycle detected")
		case 2:
			return nil
		}
		color[n] = 1
		for _, dep := range services[n].DependsOn {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		color[n] = 2
		return nil
	}

	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if color[n] == 0 {
			if err := dfs(n); err != nil {
				return err
			}
		}
	}
	return nil
}

// TopologicalOrder returns service names in dependency order (dependencies first).
func TopologicalOrder(stack *Stack) []string {
	inDeg := make(map[string]int, len(stack.Services))
	adj := make(map[string][]string)

	for name := range stack.Services {
		inDeg[name] = 0
	}
	for name, svc := range stack.Services {
		inDeg[name] = len(svc.DependsOn)
		for _, dep := range svc.DependsOn {
			adj[dep] = append(adj[dep], name)
		}
	}

	var roots []string
	for name := range stack.Services {
		if inDeg[name] == 0 {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)

	q := roots
	out := make([]string, 0, len(stack.Services))
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		out = append(out, n)

		var unlocked []string
		for _, m := range adj[n] {
			inDeg[m]--
			if inDeg[m] == 0 {
				unlocked = append(unlocked, m)
			}
		}
		sort.Strings(unlocked)
		q = append(q, unlocked...)
	}

	if len(out) < len(stack.Services) {
		// Should not happen if Parse succeeded; append remaining deterministically.
		seen := make(map[string]bool, len(out))
		for _, n := range out {
			seen[n] = true
		}
		rest := make([]string, 0)
		for n := range stack.Services {
			if !seen[n] {
				rest = append(rest, n)
			}
		}
		sort.Strings(rest)
		out = append(out, rest...)
	}

	return out
}
