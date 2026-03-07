package registry

import (
	"testing"

	"lns/internal/models"
)

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
