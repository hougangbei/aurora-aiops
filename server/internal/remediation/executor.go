package remediation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/hougangbei/aurora-aiops/server/internal/aiops"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/policy"
)

var (
	// ErrNotApproved is returned when execution starts from a non-approved state.
	ErrNotApproved = errors.New("incident is not in the approved state")
	// ErrAlreadyExecuted blocks duplicate writes: a snapshot already exists for
	// the same action, and the previous outcome is unknown.
	ErrAlreadyExecuted = errors.New("action already executed for this incident")
)

// executorClient is the minimal Kubernetes write surface the executor needs.
// Splitting it out lets tests record the write/observe order with a fake.
type executorClient interface {
	RestartDeployment(ctx context.Context, namespace, name string) error
	ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error
	SuspendCronJob(ctx context.Context, namespace, name string) error
	WaitDeploymentAvailable(ctx context.Context, namespace, name string) error
}

// KubeExecutorClient adapts the shared client-go clientset to executorClient.
type KubeExecutorClient struct {
	Client kubernetes.Interface
	// RolloutTimeout bounds WaitDeploymentAvailable.
	RolloutTimeout time.Duration
}

func (k *KubeExecutorClient) RestartDeployment(ctx context.Context, namespace, name string) error {
	deployment, err := k.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, err = k.Client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	return err
}

func (k *KubeExecutorClient) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	deployment, err := k.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	deployment.Spec.Replicas = &replicas
	_, err = k.Client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	return err
}

func (k *KubeExecutorClient) SuspendCronJob(ctx context.Context, namespace, name string) error {
	cronJob, err := k.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	suspend := true
	cronJob.Spec.Suspend = &suspend
	_, err = k.Client.BatchV1().CronJobs(namespace).Update(ctx, cronJob, metav1.UpdateOptions{})
	return err
}

// WaitDeploymentAvailable polls until the Deployment reports the Available
// condition, bounded by RolloutTimeout.
func (k *KubeExecutorClient) WaitDeploymentAvailable(ctx context.Context, namespace, name string) error {
	timeout := k.RolloutTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		deployment, err := k.Client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("observe deployment %s/%s: %w", namespace, name, err)
		}
		if isAvailable(deployment) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rollout for %s/%s did not become available within %s", namespace, name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func isAvailable(deployment *appsv1.Deployment) bool {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == "True" {
			return true
		}
	}
	return false
}

// Executor runs approved remediation actions in a strict, safe order.
type Executor struct {
	client      executorClient
	snapshotter Snapshotter
	snapshots   SnapshotStore
	audit       audit.Repository
}

// Snapshotter captures a resource snapshot before mutation.
type Snapshotter interface {
	Snapshot(ctx context.Context, incidentID, namespace, resourceKind, resourceName string) (Snapshot, error)
}

// NewExecutor returns an Executor bound to the given dependencies.
func NewExecutor(client executorClient, snapshotter Snapshotter, snapshots SnapshotStore, audit audit.Repository) *Executor {
	return &Executor{client: client, snapshotter: snapshotter, snapshots: snapshots, audit: audit}
}

// Execute performs each action in the order: verify approved -> policy re-check
// -> idempotency -> snapshot -> audit execution_started -> client-go write ->
// observe rollout -> audit result. A failed write or rollout leaves the
// snapshot in place for manual rollback.
func (e *Executor) Execute(ctx context.Context, incident aiops.Incident, actions []policy.Action, actor string) error {
	if incident.Status != aiops.StatusApproved {
		return ErrNotApproved
	}
	for _, action := range actions {
		evaluation := policy.Evaluate(action, incident.Namespace)
		if !evaluation.Allowed {
			return fmt.Errorf("%w: %s (%s)", policy.ErrActionDenied, action.Kind, evaluation.Reason)
		}

		executed, err := e.snapshots.HasExecuted(ctx, incident.ID, action.Kind, action.ResourceName)
		if err != nil {
			return err
		}
		if executed {
			// Previous outcome is unknown; do not blindly re-write.
			return ErrAlreadyExecuted
		}

		snapshot, err := e.snapshotter.Snapshot(ctx, incident.ID, action.Namespace, action.ResourceKind, action.ResourceName)
		if err != nil {
			return fmt.Errorf("snapshot before %s: %w", action.Kind, err)
		}
		snapshot.Action = action.Kind
		if err := e.snapshots.Save(ctx, snapshot); err != nil {
			return err
		}

		target := fmt.Sprintf("%s/%s/%s", action.Namespace, action.ResourceKind, action.ResourceName)
		if err := e.appendAudit(ctx, actor, "execution_started", target, snapshot.ID); err != nil {
			return err
		}

		if err := e.apply(ctx, action); err != nil {
			_ = e.appendAudit(ctx, actor, "execution_failed", target, err.Error())
			return fmt.Errorf("apply %s: %w", action.Kind, err)
		}

		if action.ResourceKind == "Deployment" {
			if err := e.client.WaitDeploymentAvailable(ctx, action.Namespace, action.ResourceName); err != nil {
				_ = e.appendAudit(ctx, actor, "execution_failed", target, "rollout not observed")
				return fmt.Errorf("observe rollout for %s: %w", action.Kind, err)
			}
		}

		if err := e.appendAudit(ctx, actor, "execution_succeeded", target, snapshot.ID); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) apply(ctx context.Context, action policy.Action) error {
	switch action.Kind {
	case policy.ActionRestartDeployment:
		return e.client.RestartDeployment(ctx, action.Namespace, action.ResourceName)
	case policy.ActionScaleDeployment:
		replicas, err := parseInt32(action.Parameters["replicas"])
		if err != nil {
			return err
		}
		return e.client.ScaleDeployment(ctx, action.Namespace, action.ResourceName, replicas)
	case policy.ActionSuspendCronJob:
		return e.client.SuspendCronJob(ctx, action.Namespace, action.ResourceName)
	default:
		return fmt.Errorf("unsupported action %q", action.Kind)
	}
}

func (e *Executor) appendAudit(ctx context.Context, actor, actionName, target, payload string) error {
	_, err := e.audit.Append(ctx, audit.Record{
		Actor:     actor,
		Action:    actionName,
		Target:    target,
		Result:    actionName,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("append audit %s: %w", actionName, err)
	}
	return nil
}

func parseInt32(value string) (int32, error) {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid replicas %q: %w", value, err)
	}
	return int32(n), nil
}
