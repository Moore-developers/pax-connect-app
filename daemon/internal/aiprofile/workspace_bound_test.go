package aiprofile

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWorkspaceResultLimitChecksUnderMutationLock(t *testing.T) {
	m, _ := claudeFixture(t)
	ids := []string{"4eb87612-9a70-49d3-88e0-a54d40245269", "5eb87612-9a70-49d3-88e0-a54d40245269"}
	for i, id := range ids {
		req := Mutation{Action: "save", OperationID: id, Profile: Profile{ID: id, Name: id, BaseURL: "https://fixture.test", Multiplier: 1}, Key: "key", ResultLimit: 4000}
		for n := 0; n < 20; n++ {
			req.Profile.ModelIDs = append(req.Profile.ModelIDs, strings.Repeat("x", 100))
		}
		_, err := m.Mutate(context.Background(), req)
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 && !errors.Is(err, Failure("result_too_large")) {
			t.Fatalf("oversized second result: %v", err)
		}
	}
	w, err := m.Query(context.Background())
	if err != nil || len(w.Profiles) != 1 || w.Profiles[0].ID != ids[0] {
		t.Fatal("rejected save changed stored profiles")
	}
}
