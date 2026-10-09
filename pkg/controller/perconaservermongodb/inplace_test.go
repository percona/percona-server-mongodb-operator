package perconaservermongodb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestOnlyResourcesDifferIgnoresInitContainers(t *testing.T) {
	tmpl := func(r corev1.ResourceRequirements) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "mongo-init", Resources: r}},
			Containers:     []corev1.Container{{Name: naming.ContainerMongod, Resources: r}},
		}}
	}
	a := tmpl(corev1.ResourceRequirements{Requests: rl("1", "2Gi")})
	b := tmpl(corev1.ResourceRequirements{Requests: rl("2", "4Gi")})

	assert.True(t, onlyResourcesDiffer(a, b, naming.ContainerMongod))
}

func TestManagedContainerConfigServer(t *testing.T) {
	assert.Equal(t, naming.ContainerMongod, managedContainer(naming.ComponentConfigSrv))
}

func TestResourcesApplied(t *testing.T) {
	t.Run("fractional memory limit reported in whole bytes", func(t *testing.T) {
		target := corev1.ResourceRequirements{
			Requests: rl("1", "2Gi"),
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: *resource.NewMilliQuantity(3435973836500, resource.DecimalSI),
			},
		}
		actual := corev1.ResourceRequirements{
			Requests: rl("1", "2Gi"),
			Limits:   rl("2", "3435973837"),
		}
		assert.True(t, resourcesApplied(actual, target))
	})

	t.Run("request defaulted from limit", func(t *testing.T) {
		target := corev1.ResourceRequirements{
			Requests: rl("", "2Gi"),
			Limits:   rl("2", "4Gi"),
		}
		actual := corev1.ResourceRequirements{
			Requests: rl("2", "2Gi"),
			Limits:   rl("2", "4Gi"),
		}
		assert.True(t, resourcesApplied(actual, target))
	})

	t.Run("different memory", func(t *testing.T) {
		target := corev1.ResourceRequirements{Requests: rl("1", "2Gi"), Limits: rl("2", "4Gi")}
		actual := corev1.ResourceRequirements{Requests: rl("1", "2Gi"), Limits: rl("2", "3Gi")}
		assert.False(t, resourcesApplied(actual, target))
	})
}

func TestRevisionTemplateReadsFromAPIReader(t *testing.T) {
	tmpl := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: naming.ContainerMongod}}}}
	raw, err := json.Marshal(map[string]any{"spec": map[string]any{"template": tmpl}})
	require.NoError(t, err)
	rev := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "rs0-abc", Namespace: "ns"},
		Data:       runtime.RawExtension{Raw: raw},
	}

	// The cached client has not seen the new revision yet.
	r := buildFakeClient()
	r.apiReader = fake.NewClientBuilder().WithObjects(rev).Build()

	got, err := r.revisionTemplate(context.Background(), "ns", "rs0-abc")
	require.NoError(t, err)
	assert.Equal(t, naming.ContainerMongod, got.Spec.Containers[0].Name)
}
