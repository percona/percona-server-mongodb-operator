package psmdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Service returns a core/v1 API Service
func Service(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec) *corev1.Service {
	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   m.Name,
		"app.kubernetes.io/replset":    replset.Name,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-" + replset.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongodPortName,
					Port:       api.MongodPort(m),
					TargetPort: intstr.FromInt(int(api.MongodPort(m))),
				},
			},
			ClusterIP:                "None",
			Selector:                 ls,
			LoadBalancerSourceRanges: replset.Expose.LoadBalancerSourceRanges,
		},
	}

	if m.CompareVersion("1.12.0") >= 0 {
		svc.Labels = ls
	}

	return svc
}

// ExternalService returns a Service object needs to serve external connections
func ExternalService(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podName string) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   m.Namespace,
			Annotations: replset.Expose.ServiceAnnotations,
		},
	}

	svc.Labels = map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   m.Name,
		"app.kubernetes.io/replset":    replset.Name,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
		"app.kubernetes.io/component":  "external-service",
	}

	svc.Spec = corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongodPortName,
				Port:       api.MongodPort(m),
				TargetPort: intstr.FromInt(int(api.MongodPort(m))),
			},
		},
		Selector: map[string]string{"statefulset.kubernetes.io/pod-name": podName},
	}

	switch replset.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.Spec.ExternalTrafficPolicy = "Local"
	case corev1.ServiceTypeLoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		svc.Spec.ExternalTrafficPolicy = "Cluster"
		svc.Spec.LoadBalancerSourceRanges = replset.Expose.LoadBalancerSourceRanges
	default:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	}

	return svc
}

type ServiceAddr struct {
	Host string
	Port int
}

func (s ServiceAddr) String() string {
	return s.Host + ":" + strconv.Itoa(s.Port)
}

func GetServiceAddr(ctx context.Context, svc corev1.Service, pod corev1.Pod, cl client.Client) (*ServiceAddr, error) {
	addr := &ServiceAddr{}

	switch svc.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		addr.Host = svc.Spec.ClusterIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeLoadBalancer:
		host, err := getIngressPoint(ctx, pod, cl)
		if err != nil {
			return nil, errors.Wrap(err, "get ingress endpoint")
		}
		addr.Host = host
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeNodePort:
		addr.Host = pod.Status.HostIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.NodePort)
		}
	}
	return addr, nil
}

func getIngressPoint(ctx context.Context, pod corev1.Pod, cl client.Client) (string, error) {
	var retries uint64 = 0

	var ip string
	var hostname string

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		retries++

		if retries >= 1000 {
			return "", errors.New("retries limit reached")
		}

		svc := &corev1.Service{}
		err := cl.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, svc)
		if err != nil {
			return "", errors.Wrap(err, "failed to fetch service")
		}

		if len(svc.Status.LoadBalancer.Ingress) != 0 {
			ip = svc.Status.LoadBalancer.Ingress[0].IP
			hostname = svc.Status.LoadBalancer.Ingress[0].Hostname
		}

		if ip != "" {
			return ip, nil
		}

		if hostname != "" {
			return hostname, nil
		}

	}

	return "", errors.Errorf("can't get service %s ingress", pod.Name)
}

// GetReplsetAddrs returns a slice of replset host:port addresses
func GetReplsetAddrs(ctx context.Context, cl client.Client, m *api.PerconaServerMongoDB, rsName string, rsExposed bool, pods []corev1.Pod) ([]string, error) {
	addrs := make([]string, 0)

	for _, pod := range pods {
		host, err := MongoHost(ctx, cl, m, rsName, rsExposed, pod)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get external hostname for pod %s", pod.Name)
		}
		addrs = append(addrs, host)
	}

	return addrs, nil
}

// MongoHost returns the mongo host for given pod
func MongoHost(ctx context.Context, cl client.Client, m *api.PerconaServerMongoDB, rsName string, rsExposed bool, pod corev1.Pod) (string, error) {
	if rsExposed {
		if m.Spec.MultiCluster.Enabled {
			return GetMCSAddr(m, pod.Name), nil
		}

		return getExtAddr(ctx, cl, m.Namespace, pod)
	}

	return GetAddr(m, pod.Name, rsName), nil
}

func getExtAddr(ctx context.Context, cl client.Client, namespace string, pod corev1.Pod) (string, error) {
	svc, err := getExtServices(ctx, cl, namespace, pod.Name)
	if err != nil {
		return "", errors.Wrap(err, "fetch service address")
	}

	hostname, err := GetServiceAddr(ctx, *svc, pod, cl)
	if err != nil {
		return "", errors.Wrap(err, "get service hostname")
	}

	return hostname.String(), nil
}

// GetAddr returns replicaSet pod address in cluster
func GetAddr(m *api.PerconaServerMongoDB, pod, replset string) string {
	return strings.Join([]string{pod, m.Name + "-" + replset, m.Namespace, m.Spec.ClusterServiceDNSSuffix}, ".") +
		":" + strconv.Itoa(int(api.MongodPort(m)))
}

// GetMCSAddr returns ReplicaSet pod address using MultiCluster FQDN
func GetMCSAddr(m *api.PerconaServerMongoDB, pod string) string {
	return fmt.Sprintf("%s.%s.%s:%d", pod, m.Namespace, m.Spec.MultiCluster.DNSSuffix, api.DefaultMongodPort)
}

func getExtServices(ctx context.Context, cl client.Client, namespace, podName string) (*corev1.Service, error) {
	svcMeta := &corev1.Service{}

	for retries := 0; retries < 6; retries++ {
		err := cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, svcMeta)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return nil, errors.Wrap(err, "failed to fetch service")
		}
		return svcMeta, nil
	}
	return nil, errors.New("failed to fetch service: retries limit reached")
}
