package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// onlineTLSRotationSupported reports whether mongod/mongos can reload TLS
// certificates with rotateCertificates (MongoDB 5.0+). An unknown version is
// treated as supported: the cluster is new and every pod starts with the
// current certificates anyway.
func onlineTLSRotationSupported(cr *api.PerconaServerMongoDB) bool {
	if cr.Status.MongoVersion == "" {
		return true
	}
	cmp, err := cr.CompareMongoDBVersion("5.0")
	if err != nil {
		return true
	}
	return cmp >= 0
}

const (
	tlsRotationNotSynced = "notsynced"
	tlsRotationUpToDate  = "uptodate"
	tlsRotationRebuilt   = "rebuilt"
)

// tlsRotationScript checks that the kubelet has synced the mounted secrets to
// the expected certificates ($1, $2: sha256 of tls.crt and of the internal
// tls.crt, empty to skip) and rebuilds the PEM files ps-entry.sh builds at
// container start.
var tlsRotationScript = fmt.Sprintf(`
set -e
synced() {
	[ -z "$2" ] && return 0
	[ "$(sha256sum "$1/tls.crt" | cut -d' ' -f1)" = "$2" ]
}
synced %[1]s "$1" && synced %[2]s "$2" || { echo %[3]s; exit 0; }
changed=0
build() {
	[ -f "$1/tls.key" ] && [ -f "$1/tls.crt" ] || return 0
	cat "$1/tls.key" "$1/tls.crt" > "$2.new"
	if [ -f "$2" ] && [ "$(sha256sum < "$2.new")" = "$(sha256sum < "$2")" ]; then
		rm -f "$2.new"
	else
		mv "$2.new" "$2"
		changed=1
	fi
}
build %[1]s /tmp/tls.pem
build %[2]s /tmp/tls-internal.pem
if [ "$changed" = 1 ]; then echo %[4]s; else echo %[5]s; fi
`, config.SSLDir, config.SSLInternalDir, tlsRotationNotSynced, tlsRotationRebuilt, tlsRotationUpToDate)

// rotateTLSInPlace makes running mongod/mongos processes load the current TLS
// leaf certificates without a restart. Pod templates only carry a hash of the
// CA (see sslAnnotation), so leaf-only changes such as SANs added for a new
// shard or a certificate renewal no longer roll the whole cluster.
func (r *ReconcilePerconaServerMongoDB) rotateTLSInPlace(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.TLSEnabled() || !onlineTLSRotationSupported(cr) {
		return nil
	}
	log := logf.FromContext(ctx).WithName("rotateTLSInPlace")

	sslSecret, err := r.getSecret(ctx, cr, api.SSLSecretName(cr))
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "get TLS secret")
	}
	internalSecret, err := r.getSecret(ctx, cr, api.SSLInternalSecretName(cr))
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get internal TLS secret")
	}

	certHash := func(s *corev1.Secret) string {
		if s == nil || len(s.Data["tls.crt"]) == 0 {
			return ""
		}
		return fmt.Sprintf("%x", sha256.Sum256(s.Data["tls.crt"]))
	}
	wantCert, wantInternalCert := certHash(sslSecret), certHash(internalSecret)
	if wantCert == "" {
		return nil
	}
	wantLeaf := wantCert + "/" + wantInternalCert

	pods := corev1.PodList{}
	if err := r.client.List(ctx, &pods, client.InNamespace(cr.Namespace),
		client.MatchingLabels{naming.LabelKubernetesInstance: cr.Name}); err != nil {
		return errors.Wrap(err, "list pods")
	}

	var errs []string
	for _, pod := range pods.Items {
		if pod.Annotations[naming.AnnotationSSLLeafHash] == wantLeaf {
			continue
		}
		container := mongoContainer(pod)
		if container == nil || !isContainerAndPodRunning(pod, container.Name) || !isPodReady(pod) {
			continue
		}

		var outb, errb bytes.Buffer
		cmd := []string{"sh", "-c", tlsRotationScript, "rotate-tls", wantCert, wantInternalCert}
		if err := r.clientcmd.Exec(ctx, &pod, container.Name, cmd, nil, &outb, &errb, false); err != nil {
			errs = append(errs, fmt.Sprintf("%s: rebuild PEM files: %v / %s", pod.Name, err, errb.String()))
			continue
		}

		switch strings.TrimSpace(outb.String()) {
		case tlsRotationNotSynced:
			log.V(1).Info("Mounted TLS secrets are not synced yet", "pod", pod.Name)
			continue
		case tlsRotationRebuilt:
			if err := r.runRotateCertificates(ctx, cr, pod, *container); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", pod.Name, err))
				continue
			}
			log.Info("TLS certificates rotated in place", "pod", pod.Name)
		}

		patch := client.MergeFrom(pod.DeepCopy())
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[naming.AnnotationSSLLeafHash] = wantLeaf
		if err := r.client.Patch(ctx, &pod, patch); err != nil {
			errs = append(errs, fmt.Sprintf("%s: annotate pod: %v", pod.Name, err))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func mongoContainer(pod corev1.Pod) *corev1.Container {
	for i, c := range pod.Spec.Containers {
		if c.Name == naming.ComponentMongod || c.Name == naming.ComponentMongos {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) runRotateCertificates(ctx context.Context, cr *api.PerconaServerMongoDB, pod corev1.Pod, container corev1.Container) error {
	if len(container.Ports) == 0 || pod.Status.PodIP == "" {
		return errors.New("pod has no address to connect to")
	}
	host := net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(container.Ports[0].ContainerPort)))

	creds, err := getInternalCredentials(ctx, r.client, cr, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "get cluster admin credentials")
	}
	cli, err := psmdb.StandaloneClient(ctx, r.client, cr, creds, host, true)
	if err != nil {
		return errors.Wrap(err, "connect")
	}
	defer func() { _ = cli.Disconnect(ctx) }()

	if err := cli.Database("admin").RunCommand(ctx, bson.D{{Key: "rotateCertificates", Value: 1}}).Err(); err != nil {
		return errors.Wrap(err, "rotateCertificates")
	}
	return nil
}
