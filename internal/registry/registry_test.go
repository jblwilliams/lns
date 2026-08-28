package registry

import (
	"sync"
	"testing"

	"lns/internal/models"
)

func TestConcurrentUpsertsDoNotLoseProjects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}

	projects := []models.Project{
		{Name: "alpha", Services: []models.Service{{Name: "web", Port: 4101, Profile: models.ProfileHMR, Status: models.StatusResolved}}},
		{Name: "beta", Services: []models.Service{{Name: "web", Port: 4102, Profile: models.ProfileHMR, Status: models.StatusResolved}}},
	}
	managers := []*Manager{first, second}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range managers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs <- managers[index].UpsertProject(projects[index])
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert: %v", err)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projects) != 2 {
		t.Fatalf("expected both concurrent projects, got %#v", loaded.Projects)
	}
}

func TestValidateProjectConflictsChecksPortAndHostnameOwnership(t *testing.T) {
	mgr := &Manager{
		Registry: &models.Registry{
			Projects: map[string]models.Project{
				"alpha": {
					Name:   "alpha",
					Prefix: "alpha",
					Services: []models.Service{
						{Name: "frontend", Root: ".", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved},
					},
				},
			},
		},
	}
	normalizeRegistry(mgr.Registry)

	project := models.Project{
		Name:   "beta",
		Prefix: "alpha",
		Services: []models.Service{
			{Name: "frontend", Root: ".", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved},
		},
	}

	errs := mgr.ValidateProjectConflicts(project)
	if len(errs) != 2 {
		t.Fatalf("expected 2 conflicts, got %d: %v", len(errs), errs)
	}
}

func TestValidateProjectConflictsRejectsIntraProjectHostnameCollision(t *testing.T) {
	mgr := &Manager{Registry: models.NewRegistry()}

	project := models.Project{
		Name: "demo",
		Services: []models.Service{
			{Name: "web", Root: ".", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved},
			{Name: "api", Root: "api", Port: 8000, Profile: models.ProfileStandard, Status: models.StatusResolved, Hostname: "demo-web.localhost"},
		},
	}

	errs := mgr.ValidateProjectConflicts(project)
	if len(errs) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %v", len(errs), errs)
	}
}

func TestValidateProjectConflictsAllowsDynamicServicesWithoutFixedPorts(t *testing.T) {
	mgr := &Manager{Registry: models.NewRegistry()}
	project := models.Project{
		Name: "demo",
		Services: []models.Service{
			{Name: "web", Root: "web", Script: "dev", Profile: models.ProfileHMR, Status: models.StatusResolved},
			{Name: "api", Root: "api", Script: "dev", Profile: models.ProfileStandard, Status: models.StatusResolved},
		},
	}

	if errs := mgr.ValidateProjectConflicts(project); len(errs) != 0 {
		t.Fatalf("dynamic services should not reserve port zero: %v", errs)
	}
}
