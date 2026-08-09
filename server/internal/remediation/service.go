package remediation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/hougangbei/aurora-aiops/server/internal/aiops"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/policy"
)

var (
	// ErrInvalidStatus marks approve/reject called on an incident that is not
	// awaiting approval.
	ErrInvalidStatus = errors.New("incident is not awaiting approval")
	// ErrNoExecutableActions marks an incident whose remediation plan has no
	// structured actions to execute.
	ErrNoExecutableActions = errors.New("remediation plan has no executable structured actions")
	// ErrActionNotAllowed marks an approval blocked by the deterministic policy
	// recheck performed immediately before the status transition.
	ErrActionNotAllowed = errors.New("remediation action not allowed")
)

// Service orchestrates approve / reject / execute / rollback of approved
// remediation, writing every decision to the append-only audit chain.
type Service struct {
	aiops     *aiops.Service
	runs      aiops.RunRepository
	kube      kubernetes.Interface
	executor  *Executor
	audit     audit.Repository
	decisions *DecisionStore
	snapshots SnapshotStore
	now       func() time.Time
}

// NewService returns a remediation Service bound to the given dependencies.
func NewService(
	aiopsService *aiops.Service,
	runs aiops.RunRepository,
	kube kubernetes.Interface,
	executor *Executor,
	audit audit.Repository,
	snapshots SnapshotStore,
	decisions *DecisionStore,
) *Service {
	return &Service{
		aiops:     aiopsService,
		runs:      runs,
		kube:      kube,
		executor:  executor,
		audit:     audit,
		decisions: decisions,
		snapshots: snapshots,
		now:       time.Now,
	}
}

// Approve transitions an awaiting-approval incident to approved and audits the
// decision.
func (s *Service) Approve(ctx context.Context, incidentID, actor, reason string) error {
	incident, err := s.aiops.Get(ctx, incidentID)
	if err != nil {
		return err
	}
	// Fresh policy recheck on the latest stored plan immediately before approval.
	// Approval is fail-closed: policy is the only source of approvability, so a
	// stored risk-review run can never green-light a plan policy now denies.
	plan, err := s.latestRemediation(ctx, incidentID)
	if err != nil {
		return err
	}
	effective := aiops.BuildEffectiveRiskReview(aiops.RiskReviewOutput{}, plan, incident.Namespace)
	if !effective.Approvable {
		return fmt.Errorf("%w: %s", ErrActionNotAllowed, strings.Join(effective.Blockers, "; "))
	}
	now := s.now().UTC()
	return s.decisions.Decide(ctx, incidentID, aiops.StatusApproved, audit.Record{
		Actor: actor, Action: "approve-remediation", Target: incidentID, Result: "approved",
		Payload: reason, Timestamp: now,
	}, now)
}

// Reject transitions an awaiting-approval incident to rejected and audits it.
func (s *Service) Reject(ctx context.Context, incidentID, actor, reason string) error {
	now := s.now().UTC()
	return s.decisions.Decide(ctx, incidentID, aiops.StatusRejected, audit.Record{
		Actor: actor, Action: "reject-remediation", Target: incidentID, Result: "rejected",
		Payload: reason, Timestamp: now,
	}, now)
}

// Execute runs the approved remediation plan. On any failure the incident moves
// to failed while the snapshots are kept for manual rollback.
func (s *Service) Execute(ctx context.Context, incidentID, actor string) error {
	incident, err := s.aiops.Get(ctx, incidentID)
	if err != nil {
		return err
	}
	if incident.Status != aiops.StatusApproved {
		return ErrNotApproved
	}

	actions, err := s.actionsFromPlan(ctx, incidentID)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		return ErrNoExecutableActions
	}

	if err := s.aiops.Advance(ctx, incidentID, aiops.StatusExecuting); err != nil {
		return err
	}
	if err := s.executor.Execute(ctx, incident, actions, actor); err != nil {
		_ = s.aiops.Advance(ctx, incidentID, aiops.StatusFailed)
		return err
	}
	return s.aiops.Advance(ctx, incidentID, aiops.StatusResolved)
}

// latestRemediation decodes the highest-attempt succeeded remediation run. It is
// the single loader used by both plan inspection and the approval-time policy
// recheck, so they can never disagree about which plan is current.
func (s *Service) latestRemediation(ctx context.Context, incidentID string) (aiops.RemediationOutput, error) {
	runs, err := s.runs.ListRuns(ctx, incidentID)
	if err != nil {
		return aiops.RemediationOutput{}, err
	}
	var latest *aiops.AgentRun
	for i := range runs {
		if runs[i].Role != "remediation" || runs[i].Status != aiops.RunStatusSucceeded {
			continue
		}
		if latest == nil || runs[i].Attempt > latest.Attempt {
			copy := runs[i]
			latest = &copy
		}
	}
	if latest == nil {
		return aiops.RemediationOutput{}, ErrNoExecutableActions
	}
	return aiops.DecodeRemediation(latest.Output)
}

// actionsFromPlan reads the latest remediation plan and converts its structured
// actions into policy.Action objects.
func (s *Service) actionsFromPlan(ctx context.Context, incidentID string) ([]policy.Action, error) {
	output, err := s.latestRemediation(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	var actions []policy.Action
	for _, action := range output.Actions {
		if action.Kind == "" {
			continue // display-only action without structured fields
		}
		actions = append(actions, policy.Action{
			Kind:         action.Kind,
			Namespace:    action.Namespace,
			ResourceKind: action.ResourceKind,
			ResourceName: action.ResourceName,
			Parameters:   action.Parameters,
		})
	}
	return actions, nil
}

// Rollback restores a resource from a previously captured snapshot and audits
// the operation.
func (s *Service) Rollback(ctx context.Context, incidentID, snapshotID, actor string) error {
	snapshot, err := s.snapshots.Get(ctx, snapshotID)
	if err != nil {
		return err
	}
	obj, gvk, err := decodeSnapshotYAML(snapshot.YAML)
	if err != nil {
		return err
	}

	switch gvk.Kind {
	case "Deployment":
		deployment, ok := obj.(*appsv1.Deployment)
		if !ok {
			return fmt.Errorf("snapshot %s is not a deployment", snapshotID)
		}
		if _, err := s.kube.AppsV1().Deployments(snapshot.Namespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("rollback deployment: %w", err)
		}
	case "CronJob":
		cronJob, ok := obj.(*batchv1.CronJob)
		if !ok {
			return fmt.Errorf("snapshot %s is not a cronjob", snapshotID)
		}
		if _, err := s.kube.BatchV1().CronJobs(snapshot.Namespace).Update(ctx, cronJob, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("rollback cronjob: %w", err)
		}
	default:
		return fmt.Errorf("rollback for kind %q not supported", gvk.Kind)
	}

	_, err = s.audit.Append(ctx, audit.Record{
		Actor: actor, Action: "rollback", Target: snapshotID, Result: "success",
		Payload: fmt.Sprintf("%s/%s", snapshot.Namespace, snapshot.Name), Timestamp: s.now().UTC(),
	})
	return err
}

var snapshotYAMLSerializer = json.NewSerializerWithOptions(
	json.DefaultMetaFactory,
	scheme.Scheme,
	scheme.Scheme,
	json.SerializerOptions{Yaml: true, Pretty: false, Strict: false},
)

func decodeSnapshotYAML(yamlText string) (runtime.Object, *schema.GroupVersionKind, error) {
	obj, gvk, err := snapshotYAMLSerializer.Decode([]byte(yamlText), nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("decode snapshot yaml: %w", err)
	}
	return obj, gvk, nil
}
