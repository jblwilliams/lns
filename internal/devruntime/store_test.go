package devruntime

import (
	"os"
	"testing"
	"time"
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

func TestWithLeasesSerializesDerivedStateWithMutations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := NewStore()
	first := Lease{Project: "demo", Service: "web", Hostname: "demo-web.localhost", Port: 4101, PID: os.Getpid()}
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	snapshotDone := make(chan error, 1)
	go func() {
		snapshotDone <- store.WithLeases(func(leases []Lease) error {
			if len(leases) != 1 || leases[0].Hostname != first.Hostname {
				t.Errorf("unexpected locked snapshot: %#v", leases)
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- store.Add(Lease{Project: "demo", Service: "api", Hostname: "demo-api.localhost", Port: 4102, PID: os.Getpid()})
	}()
	select {
	case err := <-mutationDone:
		t.Fatalf("lease mutation escaped the locked snapshot: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-snapshotDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
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
