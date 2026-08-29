package devruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"lns/internal/config"
	"lns/internal/models"
	"lns/internal/state"
)

type Lease struct {
	Project  string         `json:"project"`
	Service  string         `json:"service"`
	Root     string         `json:"root"`
	Hostname string         `json:"hostname"`
	Port     int            `json:"port"`
	PID      int            `json:"pid"`
	Worktree string         `json:"worktree,omitempty"`
	Profile  models.Profile `json:"profile,omitempty"`
	Started  time.Time      `json:"started"`
}

type Store struct {
	path string
}

func NewStore() *Store {
	return &Store{path: config.GetRuntimePath()}
}

func (s *Store) Load() ([]Lease, error) {
	var leases []Lease
	err := s.WithLeases(func(loaded []Lease) error {
		leases = loaded
		return nil
	})
	return leases, err
}

// WithLeases holds the shared state lock while fn observes the canonical live
// lease snapshot. It is used when a derived artifact, such as the Caddy route
// file, must be written atomically with respect to lease mutations.
func (s *Store) WithLeases(fn func([]Lease) error) error {
	return state.WithGlobalLock(func() error {
		leases, changed, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if changed {
			if err := s.saveUnlocked(leases); err != nil {
				return err
			}
		}
		return fn(append([]Lease(nil), leases...))
	})
}

func (s *Store) Add(lease Lease) error {
	if lease.Project == "" || lease.Service == "" || lease.Hostname == "" || lease.Port < 1 || lease.PID < 1 {
		return fmt.Errorf("invalid runtime lease")
	}
	if lease.Started.IsZero() {
		lease.Started = time.Now().UTC()
	}

	return state.WithGlobalLock(func() error {
		leases, _, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		for _, existing := range leases {
			sameLease := existing.Project == lease.Project && existing.Service == lease.Service && existing.Worktree == lease.Worktree
			if existing.Port == lease.Port && !sameLease {
				return fmt.Errorf("port %d is already leased by %s:%s", lease.Port, existing.Project, existing.Service)
			}
			if existing.Hostname == lease.Hostname && !sameLease {
				return fmt.Errorf("hostname %q is already leased by %s:%s", lease.Hostname, existing.Project, existing.Service)
			}
		}
		filtered := leases[:0]
		for _, existing := range leases {
			if existing.Project == lease.Project && existing.Service == lease.Service && existing.Worktree == lease.Worktree {
				continue
			}
			filtered = append(filtered, existing)
		}
		return s.saveUnlocked(append(filtered, lease))
	})
}

func (s *Store) Remove(project, service, worktree string, ownerPID int) error {
	return state.WithGlobalLock(func() error {
		leases, _, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		filtered := leases[:0]
		for _, lease := range leases {
			matches := lease.Project == project && lease.Service == service && lease.Worktree == worktree
			if matches && (ownerPID == 0 || lease.PID == ownerPID) {
				continue
			}
			filtered = append(filtered, lease)
		}
		return s.saveUnlocked(filtered)
	})
}

func (s *Store) loadUnlocked() ([]Lease, bool, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var leases []Lease
	if err := json.Unmarshal(data, &leases); err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", s.path, err)
	}
	alive := leases[:0]
	for _, lease := range leases {
		if processAlive(lease.PID) {
			alive = append(alive, lease)
		}
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].Hostname < alive[j].Hostname })
	return alive, len(alive) != len(leases), nil
}

func (s *Store) saveUnlocked(leases []Lease) error {
	data, err := json.MarshalIndent(leases, "", "  ")
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(s.path, append(data, '\n'), 0644)
}
