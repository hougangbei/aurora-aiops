package remediation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// ErrSnapshotDenied marks a resource kind that must never be snapshotted.
var ErrSnapshotDenied = errors.New("snapshot denied for this resource kind")

// Snapshot is a point-in-time copy of a resource taken before a mutation. It is
// captured through the Kubernetes API + serializer (never kubectl --export) so
// the YAML, GVK, UID and resourceVersion are authoritative.
type Snapshot struct {
	ID              string    `json:"id"`
	IncidentID      string    `json:"incidentId"`
	APIVersion      string    `json:"apiVersion"`
	Kind            string    `json:"kind"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	UID             string    `json:"uid"`
	ResourceVersion string    `json:"resourceVersion"`
	YAML            string    `json:"yaml"`
	CreatedAt       time.Time `json:"createdAt"`
}

var yamlSerializer = json.NewSerializerWithOptions(
	json.DefaultMetaFactory,
	scheme.Scheme,
	scheme.Scheme,
	json.SerializerOptions{Yaml: true, Pretty: false, Strict: false},
)

// SnapshotResource fetches the resource through client-go and serializes it to
// YAML. Secret objects are always refused; a snapshot failure must block any
// later mutation.
func SnapshotResource(
	ctx context.Context,
	client kubernetes.Interface,
	incidentID, namespace, resourceKind, resourceName string,
) (Snapshot, error) {
	if resourceKind == "Secret" {
		return Snapshot{}, fmt.Errorf("%w: secrets are never snapshotted", ErrSnapshotDenied)
	}

	var (
		obj     runtime.Object
		gvk     schema.GroupVersionKind
		uid     string
		version string
	)

	switch resourceKind {
	case "Deployment":
		item, err := client.AppsV1().Deployments(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return Snapshot{}, fmt.Errorf("get deployment %s/%s: %w", namespace, resourceName, err)
		}
		obj, gvk = item, appsv1.SchemeGroupVersion.WithKind("Deployment")
		uid, version = string(item.UID), item.ResourceVersion
	case "CronJob":
		item, err := client.BatchV1().CronJobs(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return Snapshot{}, fmt.Errorf("get cronjob %s/%s: %w", namespace, resourceName, err)
		}
		obj, gvk = item, batchv1.SchemeGroupVersion.WithKind("CronJob")
		uid, version = string(item.UID), item.ResourceVersion
	default:
		return Snapshot{}, fmt.Errorf("snapshot for resource kind %q not supported", resourceKind)
	}

	// Ensure the serializer emits apiVersion/kind even when the fetched object's
	// TypeMeta is empty (fake clients and some cached copies).
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	yamlText, err := encodeYAML(obj)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode %s snapshot: %w", resourceKind, err)
	}

	id, err := generateSnapshotID()
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		ID:              id,
		IncidentID:      incidentID,
		APIVersion:      gvk.GroupVersion().String(),
		Kind:            gvk.Kind,
		Namespace:       namespace,
		Name:            resourceName,
		UID:             uid,
		ResourceVersion: version,
		YAML:            yamlText,
		CreatedAt:       time.Now().UTC(),
	}, nil
}

// encodeYAML serializes a Kubernetes object to YAML via the shared scheme. The
// output carries apiVersion/kind (GVK), metadata, spec and status.
func encodeYAML(obj runtime.Object) (string, error) {
	var buf bytes.Buffer
	if err := yamlSerializer.Encode(obj, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func generateSnapshotID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "snap-" + hex.EncodeToString(b), nil
}
