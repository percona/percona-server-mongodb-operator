package perconaservermongodbrestore

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestUpdateStatefulSetForPhysicalRestore(t *testing.T) {
	ctx := context.Background()

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Backup: psmdbv1.BackupSpec{
				Image: "percona/percona-backup-mongodb:latest",
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "extra-volume",
						MountPath: "/extra",
					},
				},
			},
			ImagePullPolicy: corev1.PullIfNotPresent,
			Secrets: &psmdbv1.SecretsSpec{
				Users: "users-secret",
				SSL:   "ssl-secret",
			},
		},
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-rs0",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "my-cluster"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-cluster"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mongod",
							Image: "percona/percona-server-mongodb:latest",
						},
						{
							Name:  naming.ContainerBackupAgent,
							Image: "percona/percona-backup-agent:latest",
						},
					},
				},
			},
		},
	}

	secretTLS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Spec.Secrets.SSL,
			Namespace: cluster.Namespace,
		},
		Data: map[string][]byte{
			"ca.crt":  {},
			"tls.crt": {},
			"tls.key": {},
		},
	}

	r := fakeReconciler(cluster, sts, secretTLS)
	namespacedName := types.NamespacedName{
		Name:      sts.Name,
		Namespace: sts.Namespace,
	}

	err := r.updateStatefulSetForPhysicalRestore(ctx, cluster, namespacedName, 27017)
	assert.NoError(t, err)

	updatedSTS := &appsv1.StatefulSet{}
	err = r.client.Get(ctx, namespacedName, updatedSTS)
	assert.NoError(t, err)

	assert.Equal(t, "true", updatedSTS.Annotations[psmdbv1.AnnotationRestoreInProgress])

	for _, c := range updatedSTS.Spec.Template.Spec.Containers {
		assert.NotEqual(t, naming.ContainerBackupAgent, c.Name)
	}

	assert.True(t,
		slices.ContainsFunc(updatedSTS.Spec.Template.Spec.InitContainers, func(c corev1.Container) bool {
			return c.Name == "pbm-init"
		}))

	assert.Equal(t, "/opt/percona/physical-restore-ps-entry.sh", updatedSTS.Spec.Template.Spec.Containers[0].Command[0])

	assert.True(t,
		slices.ContainsFunc(updatedSTS.Spec.Template.Spec.Containers[0].VolumeMounts, func(c corev1.VolumeMount) bool {
			return c.MountPath == "/etc/pbm/"
		}))

	lastEnvVar := updatedSTS.Spec.Template.Spec.Containers[0].Env[len(updatedSTS.Spec.Template.Spec.Containers[0].Env)-1]
	expectedURI := "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@localhost:$(PBM_MONGODB_PORT)/?tls=true&tlsCertificateKeyFile=/tmp/tls.pem&tlsCAFile=/etc/mongodb-ssl/ca.crt&tlsInsecure=true"

	assert.Equal(t, "PBM_MONGODB_URI", lastEnvVar.Name)
	assert.Equal(t, expectedURI, lastEnvVar.Value)
}
