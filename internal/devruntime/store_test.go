package devruntime

import (
	"os"
	"testing"
)

func TestStoreKeepsLiveLeasesAndPrunesDeadOwners(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := NewStore()

	live := Lease{Project: "demo", Service: "web", Hostname: "demo.localhost", Port: 4101, PID: os.Getpid()}
	dead := Lease{Project: "demo", Service: "api", Hostname: "api.demo.localhost", Port: 4102, PID: 99999999}
	if err := store.Add(live); err != nil {
		t.Fatalf("add live lease: %v", err)
	}
	if err := store.Add(dead); err != nil {
		t.Fatalf("add dead lease: %v", err)
	}

	leases, err := store.Load()
	if err != nil {
		t.Fatalf("load leases: %v", err)
	}
	if len(leases) != 1 || leases[0].Hostname != live.Hostname {
		t.Fatalf("expected only the live lease, got %#v", leases)
	}
}

func TestStoreRejectsPortOwnedByAnotherActiveService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := NewStore()
	if err := store.Add(Lease{Project: "demo", Service: "web", Hostname: "demo-web.localhost", Port: 4101, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Lease{Project: "demo", Service: "api", Hostname: "demo-api.localhost", Port: 4101, PID: os.Getpid()}); err == nil {
		t.Fatal("expected active runtime port conflict")
	}
}
