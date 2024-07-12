package perconaservermongodb

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func readDefaultCR(t *testing.T, name, namespace string) *api.PerconaServerMongoDB {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "cr.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	cr := new(api.PerconaServerMongoDB)

	if err := yaml.Unmarshal(data, cr); err != nil {
		t.Fatal(err)
	}

	cr.Name = name
	cr.Namespace = namespace

	return cr
}

func updateObj[T client.Object](t *testing.T, obj T, f func(obj T)) T {
	t.Helper()

	f(obj)

	return obj
}
