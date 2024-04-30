package tls

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
)

func GetEffectiveTLSMode(ctx context.Context, cli client.Client, cr *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod, containerName string) (psmdbv1.TLSMode, error) {
	log := logf.FromContext(ctx)
	err := cli.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	if err != nil {
		return "", errors.Wrapf(err, "get pod/%s", pod.Name)
	}

	for _, ct := range pod.Spec.Containers {
		if ct.Name == containerName {
			for _, arg := range ct.Args {
				if strings.HasPrefix(arg, "--tlsMode") {
					tlsMode := strings.Split(arg, "=")[1]
					log.V(1).Info("TLS mode found in container args", "pod", pod.Name, "container", containerName, "mode", tlsMode, "arg", arg)
					return psmdbv1.TLSMode(tlsMode), nil
				}
			}
			break
		}
	}

	log.V(1).Info("TLS mode not found in container args, using CR spec", "container", containerName, "mode", cr.Spec.TLS.Mode)

	return cr.Spec.TLS.Mode, nil
}

func IsEnabledForReplset(ctx context.Context, client client.Client, cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) (bool, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + rs.Name + "-0",
			Namespace: cr.Namespace,
		},
	}

	mode, err := GetEffectiveTLSMode(ctx, client, cr, pod, "mongod")
	if err != nil {
		return false, err
	}

	return mode != psmdbv1.TLSModeDisabled, nil
}

func IsEnabledForMongos(ctx context.Context, client client.Client, cr *psmdbv1.PerconaServerMongoDB) (bool, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-mongos-0",
			Namespace: cr.Namespace,
		},
	}

	mode, err := GetEffectiveTLSMode(ctx, client, cr, pod, "mongos")
	if err != nil {
		return false, err
	}

	return mode != psmdbv1.TLSModeDisabled, nil
}

func IsEnabledForPod(ctx context.Context, client client.Client, cr *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod, containerName string) (bool, error) {
	mode, err := GetEffectiveTLSMode(ctx, client, cr, pod, containerName)
	if err != nil {
		return false, err
	}

	return mode != psmdbv1.TLSModeDisabled, nil
}
