package core

import (
	"errors"
	"pax-connect/daemon/internal/processidentity"
	"testing"
)

func TestReplacementRejectsForeignOwnerPIDReuseAndNonroot(t *testing.T) {
	owner := processidentity.Identity{PID: 123, UID: 501, Seconds: 10}
	old := processidentity.Identity{PID: 456, UID: 0, Seconds: 20}
	for _, test := range []struct {
		name   string
		record coreRecord
		live   processidentity.Identity
		valid  bool
	}{
		{"same App", coreRecord{Owner: owner, Core: old}, old, true},
		{"another App", coreRecord{Owner: processidentity.Identity{PID: 789, UID: 501}, Core: old}, old, false},
		{"App PID reused", coreRecord{Owner: processidentity.Identity{PID: 123, UID: 501, Seconds: 9}, Core: old}, old, false},
		{"core PID reused", coreRecord{Owner: owner, Core: old}, processidentity.Identity{PID: 456, UID: 0, Seconds: 21}, false},
		{"user process", coreRecord{Owner: owner, Core: processidentity.Identity{PID: 456, UID: 501}}, processidentity.Identity{PID: 456, UID: 501}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := replacementIdentity(test.record, owner, func(int) (processidentity.Identity, error) { return test.live, nil })
			if (err == nil) != test.valid {
				t.Fatalf("replacement identity accepted=%v", err == nil)
			}
		})
	}
	if replacementIdentity(coreRecord{Owner: owner, Core: old}, owner, func(int) (processidentity.Identity, error) { return old, errors.New("denied") }) == nil {
		t.Fatal("observation failure authorized a signal")
	}
}
