package deployment

import (
	"errors"
	"reflect"
	"testing"
)

func TestCatalogRejectsInvalidProjects(t *testing.T) {
	cases := []Project{
		{},
		{ID: "p", Versions: []string{"2", "1"}, SupportedArchitectures: []string{"amd64"}},
		{ID: "p", Versions: []string{"1", "1"}, SupportedArchitectures: []string{"amd64"}},
		{ID: "p", Versions: []string{"1"}},
	}
	for _, project := range cases {
		var catalog Catalog
		if err := catalog.Register(testInstaller{project: project}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Register(%+v) error=%v, want ErrInvalidInput", project, err)
		}
	}

	var catalog Catalog
	installer := testInstaller{project: Project{ID: "p", Name: "P", Versions: []string{"1", "2"}, SupportedArchitectures: []string{"amd64"}}}
	if err := catalog.Register(installer); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register(installer); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate=%v", err)
	}
}

func TestCatalogReturnsSortedCopies(t *testing.T) {
	var catalog Catalog
	if err := catalog.Register(testInstaller{project: Project{ID: "z", Versions: []string{"1", "2"}, SupportedOSFamilies: []string{"ubuntu", "debian"}, SupportedArchitectures: []string{"arm64", "amd64"}}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register(testInstaller{project: Project{ID: "a", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}}); err != nil {
		t.Fatal(err)
	}
	got, ok := catalog.Get("z")
	if !ok {
		t.Fatal("Get missing")
	}
	got.Versions[0] = "mutated"
	again, _ := catalog.Get("z")
	if again.Versions[0] != "1" {
		t.Fatalf("Get leaked alias: %+v", again)
	}
	projects := catalog.List()
	if ids := []string{projects[0].ID, projects[1].ID}; !reflect.DeepEqual(ids, []string{"a", "z"}) {
		t.Fatalf("List order=%v", ids)
	}
	projects[1].SupportedArchitectures[0] = "mutated"
	again, _ = catalog.Get("z")
	if again.SupportedArchitectures[0] != "amd64" {
		t.Fatalf("List leaked alias: %+v", again)
	}
}
