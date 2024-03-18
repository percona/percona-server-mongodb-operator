package pbm

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

type PBM struct {
	execClient    *clientcmd.Client
	k8sClient     client.Client
	pbmPath       string
	containerName string
	pod           *corev1.Pod
}

type Option = func(c *PBM)

func New(ctx context.Context, execClient *clientcmd.Client, k8sClient client.Client, cr *psmdbv1.PerconaServerMongoDB, opts ...Option) (*PBM, error) {
	p := &PBM{
		execClient:    execClient,
		k8sClient:     k8sClient,
		pbmPath:       "/usr/bin/pbm",
		containerName: "backup-agent",
	}

	for _, opt := range opts {
		opt(p)
	}

	if p.pod == nil {
		pod, err := psmdb.GetOneReadyRSPod(ctx, k8sClient, cr, cr.Spec.Replsets[0].Name)
		if err != nil {
			return &PBM{}, err
		}
		p.pod = pod
	}

	return p, nil
}

func WithPBMPath(pbmPath string) Option {
	return func(c *PBM) {
		c.pbmPath = pbmPath
	}
}

func WithContainerName(containerName string) Option {
	return func(c *PBM) {
		c.containerName = containerName
	}
}

func WithPod(pod *corev1.Pod) Option {
	return func(c *PBM) {
		c.pod = pod
	}
}
