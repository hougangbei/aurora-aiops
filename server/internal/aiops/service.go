package aiops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrInvalidIncidentRequest = errors.New("invalid incident request")
)

// CreateIncidentInput holds the fields required to create a new incident.
type CreateIncidentInput struct {
	Summary      string
	Severity     string
	Namespace    string
	ResourceKind string
	ResourceName string
}

// Service implements the core business logic for incident management.
type Service struct {
	repo IncidentRepository
}

// NewService returns a Service backed by the given repository.
func NewService(repo IncidentRepository) *Service {
	return &Service{repo: repo}
}

// Create validates the input, generates an ID, and persists a new incident.
func (s *Service) Create(ctx context.Context, input CreateIncidentInput) (Incident, error) {
	if input.Summary == "" {
		return Incident{}, ErrInvalidIncidentRequest
	}
	severity, ok := parseSeverity(input.Severity)
	if !ok {
		return Incident{}, ErrInvalidIncidentRequest
	}
	if input.Namespace == "" || input.ResourceKind == "" || input.ResourceName == "" {
		return Incident{}, ErrInvalidIncidentRequest
	}

	id, err := generateIncidentID()
	if err != nil {
		return Incident{}, err
	}

	now := time.Now().UTC()
	inc := Incident{
		ID:           id,
		Summary:      input.Summary,
		Severity:     severity,
		Status:       StatusReceived,
		Namespace:    input.Namespace,
		ResourceKind: input.ResourceKind,
		ResourceName: input.ResourceName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.Create(ctx, inc); err != nil {
		return Incident{}, err
	}
	return inc, nil
}

// Advance moves an incident to a new status if the transition is allowed.
// The validation read preserves ErrInvalidStateTransition; a compare-and-swap
// at the repository boundary then closes the race and may return
// ErrStateTransitionConflict if a concurrent decision won.
func (s *Service) Advance(ctx context.Context, id string, to Status) error {
	incident, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !CanTransition(incident.Status, to) {
		return ErrInvalidStateTransition
	}
	return s.repo.TransitionStatus(ctx, id, incident.Status, to, time.Now().UTC())
}

// Decide resolves an awaiting-approval incident to a terminal approved or
// rejected state through a single compare-and-swap. The expected "from" state
// is fixed (awaiting_approval), so concurrent decisions are arbitrated entirely
// by the CAS: exactly one caller wins and every loser observes
// ErrStateTransitionConflict instead of a stale read of the winner's new state.
func (s *Service) Decide(ctx context.Context, id string, to Status) error {
	if to != StatusApproved && to != StatusRejected {
		return ErrInvalidStateTransition
	}
	return s.repo.TransitionStatus(ctx, id, StatusAwaitingApproval, to, time.Now().UTC())
}

// Get retrieves an incident by ID.
func (s *Service) Get(ctx context.Context, id string) (Incident, error) {
	return s.repo.Get(ctx, id)
}

// List returns incidents matching the given filter.
func (s *Service) List(ctx context.Context, filter IncidentFilter) ([]Incident, error) {
	return s.repo.List(ctx, filter)
}

// parseSeverity maps a string to a Severity, returning false if invalid.
func parseSeverity(s string) (Severity, bool) {
	switch Severity(s) {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return Severity(s), true
	default:
		return "", false
	}
}

// generateIncidentID returns a random incident ID of the form "inc-<hex>".
func generateIncidentID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "inc-" + hex.EncodeToString(b), nil
}
