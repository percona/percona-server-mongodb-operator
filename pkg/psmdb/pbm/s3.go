package pbm

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const (
	AWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY"
)

func GetS3Crendentials(ctx context.Context, k8sclient client.Client, namespace string, storage psmdbv1.BackupStorageS3Spec) (s3.Credentials, error) {
	credentials := s3.Credentials{}

	// if credentials secrets is empty, we assume that user wants to use EC2 metadata
	if storage.CredentialsSecret != "" {
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      storage.CredentialsSecret,
				Namespace: namespace,
			},
		}

		if err := k8sclient.Get(ctx, client.ObjectKeyFromObject(&secret), &secret); err != nil {
			return credentials, errors.Wrapf(err, "cannot get S3 credentials secret %s/%s", namespace, storage.CredentialsSecret)
		}

		credentials.AccessKeyID = string(secret.Data[AWSAccessKeyID])
		credentials.SecretAccessKey = string(secret.Data[AWSSecretAccessKey])
	}

	return credentials, nil
}

func NewS3Config(storage psmdbv1.BackupStorageS3Spec, credentials s3.Credentials) s3.Conf {
	var awsSSE *s3.AWSsse = nil
	if len(storage.ServerSideEncryption.SSECustomerAlgorithm) != 0 && len(storage.ServerSideEncryption.SSECustomerKey) != 0 {
		awsSSE = &s3.AWSsse{
			SseCustomerAlgorithm: storage.ServerSideEncryption.SSECustomerAlgorithm,
			SseCustomerKey:       storage.ServerSideEncryption.SSECustomerKey,
		}
	}

	if len(storage.ServerSideEncryption.SSEAlgorithm) != 0 && len(storage.ServerSideEncryption.KMSKeyID) != 0 {
		awsSSE = &s3.AWSsse{
			SseAlgorithm: storage.ServerSideEncryption.SSEAlgorithm,
			KmsKeyID:     storage.ServerSideEncryption.KMSKeyID,
		}
	}

	return s3.Conf{
		EndpointURL:           storage.EndpointURL,
		Region:                storage.Region,
		Bucket:                storage.Bucket,
		Prefix:                storage.Prefix,
		UploadPartSize:        storage.UploadPartSize,
		MaxUploadParts:        storage.MaxUploadParts,
		StorageClass:          storage.StorageClass,
		InsecureSkipTLSVerify: storage.InsecureSkipTLSVerify,
		Credentials:           credentials,
		ServerSideEncryption:  awsSSE,
	}
}
