package psmdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// Service returns a core/v1 API Service
func Service(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) *corev1.Service {
	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
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
			Name:        cr.Name + "-" + replset.Name,
			Namespace:   cr.Namespace,
			Annotations: replset.Expose.ServiceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongodPortName,
					Port:       api.MongodPort(cr),
					TargetPort: intstr.FromInt(int(api.MongodPort(cr))),
				},
			},
			ClusterIP:                "None",
			Selector:                 ls,
			LoadBalancerSourceRanges: replset.Expose.LoadBalancerSourceRanges,
		},
	}

	if cr.CompareVersion("1.12.0") >= 0 {
		svc.Labels = make(map[string]string)
		for k, v := range ls {
			svc.Labels[k] = v
		}
		for k, v := range replset.Expose.ServiceLabels {
			if _, ok := svc.Labels[k]; !ok {
				svc.Labels[k] = v
			}
		}
	}

	return svc
}

// ExternalService returns a Service object needs to serve external connections
func ExternalService(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podName string) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   cr.Namespace,
			Annotations: replset.Expose.ServiceAnnotations,
		},
	}

	svc.Labels = map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/replset":    replset.Name,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
		"app.kubernetes.io/component":  "external-service",
	}

	for k, v := range replset.Expose.ServiceLabels {
		if _, ok := svc.Labels[k]; !ok {
			svc.Labels[k] = v
		}
	}

	svc.Spec = corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongodPortName,
				Port:       api.MongodPort(cr),
				TargetPort: intstr.FromInt(int(api.MongodPort(cr))),
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

var ErrNoIngressPoints = errors.New("ingress points not found")

func getIngressPoint(ctx context.Context, pod corev1.Pod, cl client.Client) (string, error) {
	var retries uint64 = 0

	var ip string
	var hostname string

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		retries++

		if retries >= 1000 {
			return "", ErrNoIngressPoints
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

	return "", ErrNoIngressPoints
}

// GetReplsetAddrs returns a slice of replset host:port addresses
func GetReplsetAddrs(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, rsName string, rsExposed bool, pods []corev1.Pod) ([]string, error) {
	addrs := make([]string, 0)

	for _, pod := range pods {
		host, err := MongoHost(ctx, cl, cr, rsName, rsExposed, pod)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get external hostname for pod %s", pod.Name)
		}
		addrs = append(addrs, host)
	}

	return addrs, nil
}

// GetMongosAddrs returns a slice of mongos addresses
func GetMongosAddrs(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) ([]string, error) {
	if !cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		host, err := MongosHost(ctx, cl, cr, nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get mongos host")
		}
		return []string{host}, nil
	}
	list, err := GetMongosPods(ctx, cl, cr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list mongos pods")
	}

	addrs := make([]string, 0, len(list.Items))
	for _, pod := range list.Items {
		host, err := MongosHost(ctx, cl, cr, &pod)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get mongos host")
		}
		addrs = append(addrs, host)
	}

	return addrs, nil
}

// MongoHost returns the mongo host for given pod
func MongoHost(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, rsName string, rsExposed bool, pod corev1.Pod) (string, error) {
	switch cr.Spec.ClusterServiceDNSMode {
	case api.DNSModeServiceMesh:
		return GetServiceMeshAddr(cr, pod.Name, cr.Namespace), nil
	case api.DNSModeInternal:
		if rsExposed && cr.MCSEnabled() {
			imported, err := IsServiceImported(ctx, cl, cr, pod.Name)
			if err != nil {
				return "", errors.Wrapf(err, "check if service imported for %s", pod.Name)
			}

			if !imported {
				return GetAddr(cr, pod.Name, rsName), nil
			}

			return GetMCSAddr(cr, pod.Name), nil
		}

		return GetAddr(cr, pod.Name, rsName), nil
	case api.DNSModeExternal:
		if rsExposed {
			if cr.MCSEnabled() {
				imported, err := IsServiceImported(ctx, cl, cr, pod.Name)
				if err != nil {
					return "", errors.Wrapf(err, "check if service imported for %s", pod.Name)
				}

				if !imported {
					return getExtAddr(ctx, cl, cr.Namespace, pod)
				}

				return GetMCSAddr(cr, pod.Name), nil
			}

			return getExtAddr(ctx, cl, cr.Namespace, pod)
		}

		return GetAddr(cr, pod.Name, rsName), nil
	default:
		return GetAddr(cr, pod.Name, rsName), nil
	}
}

// MongosHost returns the mongos host for given pod
func MongosHost(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, pod *corev1.Pod) (string, error) {
	svcName := cr.Name + "-mongos"
	if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		svcName = pod.Name
	}
	svc := new(corev1.Service)
	err := cl.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      svcName,
		}, svc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}

		return "", errors.Wrap(err, "failed to get mongos service")
	}

	var host string
	if mongos := cr.Spec.Sharding.Mongos; mongos.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
		for _, i := range svc.Status.LoadBalancer.Ingress {
			host = i.IP
			if len(i.Hostname) > 0 {
				host = i.Hostname
			}
		}
	} else {
		host = svc.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix
	}
	return host, nil
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
func GetAddr(cr *api.PerconaServerMongoDB, pod, replset string) string {
	return strings.Join([]string{pod, cr.Name + "-" + replset, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
		":" + strconv.Itoa(int(api.MongodPort(cr)))
}

// GetAddr returns replicaSet pod address in a service mesh
func GetServiceMeshAddr(cr *api.PerconaServerMongoDB, pod, replset string) string {
	return strings.Join([]string{pod, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
		":" + strconv.Itoa(int(api.MongodPort(cr)))
}

// GetMCSAddr returns ReplicaSet pod address using MultiCluster FQDN
func GetMCSAddr(cr *api.PerconaServerMongoDB, pod string) string {
	return fmt.Sprintf("%s.%s.%s:%d", pod, cr.Namespace, cr.Spec.MultiCluster.DNSSuffix, api.DefaultMongodPort)
}

var ErrServiceNotExists = errors.New("service doesn't exist")

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
	return nil, ErrServiceNotExists
}

func IsServiceImported(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, svcName string) (bool, error) {
	si := new(mcsv1alpha1.ServiceImport)
	err := k8sclient.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      svcName,
	}, si)
	if err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return true, nil
}
