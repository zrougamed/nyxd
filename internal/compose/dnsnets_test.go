package compose

import "testing"

func TestServiceUsesOnlyInternalNetworks_explicit(t *testing.T) {
	st := &Stack{
		Networks: map[string]Network{
			"front": {Internal: true},
			"back":  {Internal: false},
		},
		Services: map[string]Service{
			"a": {Networks: []string{"front"}},
			"b": {Networks: []string{"front", "back"}},
		},
	}
	if !ServiceUsesOnlyInternalNetworks(st, st.Services["a"]) {
		t.Fatal("a should be internal-only")
	}
	if ServiceUsesOnlyInternalNetworks(st, st.Services["b"]) {
		t.Fatal("b has external network")
	}
}

func TestServiceUsesOnlyInternalNetworks_defaultKey(t *testing.T) {
	st := &Stack{
		Networks: map[string]Network{
			"default": {Internal: true},
		},
		Services: map[string]Service{"web": {}},
	}
	if !ServiceUsesOnlyInternalNetworks(st, st.Services["web"]) {
		t.Fatal("implicit default internal")
	}
}

func TestServiceUsesOnlyInternalNetworks_singleNamed(t *testing.T) {
	st := &Stack{
		Networks: map[string]Network{
			"lan": {Internal: true},
		},
		Services: map[string]Service{"web": {}},
	}
	if !ServiceUsesOnlyInternalNetworks(st, st.Services["web"]) {
		t.Fatal("single declared network should apply when service has no networks list")
	}
}
