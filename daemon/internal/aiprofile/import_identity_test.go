package aiprofile

import (
	"context"
	"errors"
	"testing"
)

func TestImportStableIDCannotOverwriteExistingProfile(t *testing.T) {
	m, _ := claudeFixture(t)
	w, err := m.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := Mutation{Action: "import", CLI: Claude, InspectionID: w.Bindings[1].InspectionID, OperationID: "import-one", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Imported", Multiplier: 1}}
	w, err = m.Mutate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Profiles) != 1 || w.Profiles[0].ID != req.Profile.ID {
		t.Fatal(w)
	}
	req.OperationID = "import-again"
	w, err = m.Mutate(context.Background(), req)
	if err != nil || len(w.Profiles) != 1 {
		t.Fatalf("duplicate import: %v", err)
	}
	req.Profile.Name = "Different"
	if _, err = m.Mutate(context.Background(), req); !errors.Is(err, Conflict) {
		t.Fatalf("ID overwritten: %v", err)
	}
	w, err = m.Query(context.Background())
	if err != nil || w.Profiles[0].Name != "Imported" {
		t.Fatal("saved profile changed")
	}
}

func TestSaveStableIDSameContentDoesNotCreateOrBump(t *testing.T) {
	m, _ := claudeFixture(t)
	req := Mutation{Action: "save", OperationID: "first", Profile: Profile{ID: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "Saved", BaseURL: "https://fixture.test", Multiplier: 1}, Key: "key"}
	w, err := m.Mutate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.OperationID = "second"
	again, err := m.Mutate(context.Background(), req)
	if err != nil || len(again.Profiles) != 1 || again.Profiles[0].Revision != w.Profiles[0].Revision {
		t.Fatalf("duplicate create: %v", err)
	}
	req.Profile.BaseURL = "https://other.test"
	if _, err = m.Mutate(context.Background(), req); !errors.Is(err, Conflict) {
		t.Fatal(err)
	}
}
