package projectconfig

import (
	"testing"

	"lns/internal/models"
)

func TestValidateAcceptsResolvedAndUnresolvedServices(t *testing.T) {
	cfg := &Config{
		Name: "demo",
		Services: map[string]Service{
			"frontend": {
				Root:    ".",
				Port:    5179,
				Profile: models.ProfileHMR,
				Status:  models.StatusResolved,
			},
			"api": {
				Root:   "api",
				Status: models.StatusUnresolved,
			},
		},
	}

	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("expected valid config, got %v", errs)
	}
}

func TestValidateRejectsInvalidResolvedService(t *testing.T) {
	cfg := &Config{
		Name: "demo",
		Services: map[string]Service{
			"frontend": {
				Root:   ".",
				Status: models.StatusResolved,
			},
		},
	}

	if errs := Validate(cfg); len(errs) != 2 {
		t.Fatalf("expected 2 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateRejectsResolvedHostnameCollisionAgainstDerivedHostname(t *testing.T) {
	cfg := &Config{
		Name: "demo",
		Services: map[string]Service{
			"web": {
				Root:    ".",
				Port:    5179,
				Profile: models.ProfileHMR,
				Status:  models.StatusResolved,
			},
			"api": {
				Root:     "api",
				Port:     8000,
				Profile:  models.ProfileStandard,
				Status:   models.StatusResolved,
				Hostname: "demo-web.localhost",
			},
		},
	}

	errs := Validate(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].Field != "hostname" {
		t.Fatalf("expected hostname validation error, got %v", errs[0])
	}
}

func TestValidateRejectsCaseInsensitiveHostnameCollisions(t *testing.T) {
	cfg := &Config{
		Name: "demo",
		Services: map[string]Service{
			"web": {
				Root:     ".",
				Port:     5179,
				Profile:  models.ProfileHMR,
				Status:   models.StatusResolved,
				Hostname: "Foo.localhost",
			},
			"api": {
				Root:     "api",
				Port:     8000,
				Profile:  models.ProfileStandard,
				Status:   models.StatusResolved,
				Hostname: "foo.localhost",
			},
		},
	}

	errs := Validate(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].Field != "hostname" {
		t.Fatalf("expected hostname validation error, got %v", errs[0])
	}
}

func TestValidateRejectsInvalidExplicitHostname(t *testing.T) {
	cases := []string{
		"https://app.localhost",
		"app.localhost:3000",
		"foo bar",
	}

	for _, hostname := range cases {
		cfg := &Config{
			Name: "demo",
			Services: map[string]Service{
				"web": {
					Root:     ".",
					Port:     5179,
					Profile:  models.ProfileHMR,
					Status:   models.StatusResolved,
					Hostname: hostname,
				},
			},
		}

		errs := Validate(cfg)
		if len(errs) != 1 {
			t.Fatalf("hostname %q: expected 1 validation error, got %d: %v", hostname, len(errs), errs)
		}
		if errs[0].Field != "hostname" {
			t.Fatalf("hostname %q: expected hostname validation error, got %v", hostname, errs[0])
		}
	}
}
