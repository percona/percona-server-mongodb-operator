package pbm

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const (
	AzureStorageAccountName = "AZURE_STORAGE_ACCOUNT_NAME"
	AzureStorageAccountKey  = "AZURE_STORAGE_ACCOUNT_KEY"
)

func GetAzureCrendentials(ctx context.Context, k8sclient client.Client, namespace string, storage psmdbv1.BackupStorageAzureSpec) (string, azure.Credentials, error) {
	credentials := azure.Credentials{}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      storage.CredentialsSecret,
			Namespace: namespace,
		},
	}

	if err := k8sclient.Get(ctx, client.ObjectKeyFromObject(&secret), &secret); err != nil {
		return "", credentials, errors.Wrapf(err, "cannot get azure credentials secret %s/%s", namespace, storage.CredentialsSecret)
	}

	credentials.Key = string(secret.Data[AzureStorageAccountKey])

	return string(secret.Data[AzureStorageAccountName]), credentials, nil
}

func NewAzureConfig(storage psmdbv1.BackupStorageAzureSpec, account string, credentials azure.Credentials) azure.Conf {
	return azure.Conf{
		Account:     account,
		Container:   storage.Container,
		Prefix:      storage.Prefix,
		Credentials: credentials,
	}
}
