package compose

import "testing"

func TestReverseOrderedContainerIDs(t *testing.T) {
	st := &Stack{
		Services: map[string]Service{
			"app": {Image: "alpine", DependsOn: DependsOn{"db"}},
			"db":  {Image: "alpine"},
		},
	}
	rev := ReverseOrderedContainerIDs(st, "proj")
	if len(rev) != 2 {
		t.Fatalf("len %d", len(rev))
	}
	// start order: db, app → reverse: app, db
	want0 := composeContainerID("proj", "app")
	want1 := composeContainerID("proj", "db")
	if rev[0] != want0 || rev[1] != want1 {
		t.Fatalf("got %#v want [%s %s]", rev, want0, want1)
	}
}
