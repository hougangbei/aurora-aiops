package remediation

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestSnapshotDeployment(t *testing.T) {
	replicas := int32(3)
	fake := kubefake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "default", UID: types.UID("uid-1"), ResourceVersion: "42",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "nginx:1.27"}}},
			},
		},
	})

	snap, err := SnapshotResource(context.Background(), fake, "inc-1", "default", "Deployment", "api")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "Deployment" || snap.APIVersion != "apps/v1" {
		t.Fatalf("gvk: kind=%s api=%s", snap.Kind, snap.APIVersion)
	}
	if snap.UID != "uid-1" || snap.ResourceVersion != "42" {
		t.Fatalf("uid=%s rv=%s", snap.UID, snap.ResourceVersion)
	}
	for _, needle := range []string{"kind: Deployment", "apiVersion: apps/v1", "replicas: 3", "nginx:1.27"} {
		if !strings.Contains(snap.YAML, needle) {
			t.Fatalf("yaml missing %q:\n%s", needle, snap.YAML)
		}
	}
	if snap.ID == "" || snap.IncidentID != "inc-1" || snap.CreatedAt.IsZero() {
		t.Fatalf("snapshot metadata incomplete: %+v", snap)
	}
}

func TestSnapshotCronJob(t *testing.T) {
	fake := kubefake.NewSimpleClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backup", Namespace: "default", UID: types.UID("uid-cj"), ResourceVersion: "7",
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 2 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "backup", Image: "busybox:1.36"}}},
					},
				},
			},
		},
	})

	snap, err := SnapshotResource(context.Background(), fake, "inc-1", "default", "CronJob", "backup")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "CronJob" || snap.APIVersion != "batch/v1" {
		t.Fatalf("gvk: kind=%s api=%s", snap.Kind, snap.APIVersion)
	}
	if snap.UID != "uid-cj" || snap.ResourceVersion != "7" {
		t.Fatalf("uid=%s rv=%s", snap.UID, snap.ResourceVersion)
	}
	for _, needle := range []string{"kind: CronJob", "schedule: 0 2 * * *"} {
		if !strings.Contains(snap.YAML, needle) {
			t.Fatalf("yaml missing %q:\n%s", needle, snap.YAML)
		}
	}
}

func TestSnapshotRejectsSecret(t *testing.T) {
	fake := kubefake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default", UID: types.UID("uid-secret")},
	})
	_, err := SnapshotResource(context.Background(), fake, "inc-1", "default", "Secret", "creds")
	if !errors.Is(err, ErrSnapshotDenied) {
		t.Fatalf("err=%v want ErrSnapshotDenied", err)
	}
}

func TestSnapshotMissingResourceFails(t *testing.T) {
	fake := kubefake.NewSimpleClientset()
	if _, err := SnapshotResource(context.Background(), fake, "inc-1", "default", "Deployment", "missing"); err == nil {
		t.Fatal("expected error for missing resource")
	}
}

func TestSnapshotUnsupportedKindFails(t *testing.T) {
	fake := kubefake.NewSimpleClientset()
	if _, err := SnapshotResource(context.Background(), fake, "inc-1", "default", "DaemonSet", "x"); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}
