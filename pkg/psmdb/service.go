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
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// Service returns a core/v1 API Service
func Service(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) *corev1.Service {
	ls := naming.ServiceLabels(cr, replset)

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
					Name:       config.MongodPortName,
					Port:       replset.GetPort(),
					TargetPort: intstr.FromInt(int(replset.GetPort())),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}

	svc.Labels = make(map[string]string)
	for k, v := range ls {
		svc.Labels[k] = v
	}
	for k, v := range replset.Expose.ServiceLabels {
		if _, ok := svc.Labels[k]; !ok {
			svc.Labels[k] = v
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

	svc.Labels = naming.ExternalServiceLabels(cr, replset)

	for k, v := range replset.Expose.ServiceLabels {
		if _, ok := svc.Labels[k]; !ok {
			svc.Labels[k] = v
		}
	}

	svc.Spec = corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       config.MongodPortName,
				Port:       replset.GetPort(),
				TargetPort: intstr.FromInt(int(replset.GetPort())),
			},
		},
		Selector:                 map[string]string{"statefulset.kubernetes.io/pod-name": podName},
		PublishNotReadyAddresses: true,
		InternalTrafficPolicy:    replset.Expose.InternalTrafficPolicy,
		ExternalTrafficPolicy:    replset.Expose.ExternalTrafficPolicy,
	}

	switch replset.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if len(replset.Expose.ExternalTrafficPolicy) != 0 {
			svc.Spec.ExternalTrafficPolicy = replset.Expose.ExternalTrafficPolicy
		} else {
			svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeCluster
		}

		if cr.CompareVersion("1.19.0") < 0 {
			svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeLocal
		}
	case corev1.ServiceTypeLoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		if len(replset.Expose.ExternalTrafficPolicy) != 0 {
			svc.Spec.ExternalTrafficPolicy = replset.Expose.ExternalTrafficPolicy
		} else {
			svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeLocal
		}

		if cr.CompareVersion("1.19.0") < 0 {
			svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeCluster
		}
		svc.Spec.LoadBalancerSourceRanges = replset.Expose.LoadBalancerSourceRanges
		if cr.CompareVersion("1.20.0") >= 0 {
			svc.Spec.LoadBalancerClass = replset.Expose.LoadBalancerClass
		}
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
			if p.Name != config.MongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeLoadBalancer:
		host, err := getServiceIngressAddr(ctx, cl, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
		if err != nil {
			return nil, errors.Wrap(err, "get ingress endpoint")
		}
		addr.Host = host
		for _, p := range svc.Spec.Ports {
			if p.Name != config.MongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeNodePort:
		addr.Host = pod.Status.HostIP
		for _, p := range svc.Spec.Ports {
			if p.Name != config.MongodPortName {
				continue
			}
			addr.Port = int(p.NodePort)
		}
	}
	return addr, nil
}

var ErrNoIngressPoints = errors.New("ingress points not found")

func getServiceIngressAddr(ctx context.Context, cl client.Client, serviceNN types.NamespacedName) (string, error) {
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
		err := cl.Get(ctx, serviceNN, svc)
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

func getServiceClusterIP(ctx context.Context, cl client.Client, serviceNN types.NamespacedName) (string, error) {
	svc := &corev1.Service{}
	err := cl.Get(ctx, serviceNN, svc)
	if err != nil {
		return "", errors.Wrap(err, "failed to fetch service")
	}

	return svc.Spec.ClusterIP, nil
}

// GetReplsetAddrs returns a slice of replset host:port addresses
func GetReplsetAddrs(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, dnsMode api.DNSMode, rs *api.ReplsetSpec, rsExposed bool, pods []corev1.Pod) ([]string, error) {
	addrs := make([]string, 0)

	for _, pod := range pods {
		host, err := MongoHost(ctx, cl, cr, dnsMode, rs, rsExposed, pod)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get external hostname for pod %s", pod.Name)
		}
		addrs = append(addrs, host)
	}

	return addrs, nil
}

// GetMongosAddrs returns a slice of mongos addresses
func GetMongosAddrs(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, useInternalAddr bool) ([]string, error) {
	if !cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		host, err := MongosHost(ctx, cl, cr, nil, useInternalAddr)
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
		host, err := MongosHost(ctx, cl, cr, &pod, useInternalAddr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get mongos host")
		}
		addrs = append(addrs, host)
	}

	return addrs, nil
}

// MongoHost returns the mongo host for given pod
func MongoHost(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, dnsMode api.DNSMode, replset *api.ReplsetSpec, rsExposed bool, pod corev1.Pod) (string, error) {
	overrides := replset.ReplsetOverrides[pod.Name]
	if len(overrides.Host) > 0 {
		if strings.Contains(overrides.Host, ":") {
			return overrides.Host, nil
		}

		return fmt.Sprintf("%s:%d", overrides.Host, replset.GetPort()), nil
	}

	switch dnsMode {
	case api.DNSModeServiceMesh:
		return GetServiceMeshAddr(cr, pod.Name, replset.GetPort()), nil
	case api.DNSModeInternal:
		if rsExposed && cr.MCSEnabled() {
			imported, err := IsServiceImported(ctx, cl, cr, pod.Name)
			if err != nil {
				return "", errors.Wrapf(err, "check if service imported for %s", pod.Name)
			}

			if !imported {
				return GetAddr(cr, pod.Name, replset.Name, replset.GetPort()), nil
			}

			return GetMCSAddr(cr, pod.Name, replset.GetPort()), nil
		}

		return GetAddr(cr, pod.Name, replset.Name, replset.GetPort()), nil
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

				return GetMCSAddr(cr, pod.Name, replset.GetPort()), nil
			}

			return getExtAddr(ctx, cl, cr.Namespace, pod)
		}

		return GetAddr(cr, pod.Name, replset.Name, replset.GetPort()), nil
	default:
		return GetAddr(cr, pod.Name, replset.Name, replset.GetPort()), nil
	}
}

// MongosHost returns the mongos host for given pod
func MongosHost(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, pod *corev1.Pod, useInternalAddr bool) (string, error) {
	svcName := cr.Name + "-mongos"
	if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		svcName = pod.Name
	}

	nn := types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      svcName,
	}

	svc := new(corev1.Service)
	err := cl.Get(ctx, nn, svc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}

		return "", errors.Wrap(err, "failed to get mongos service")
	}

	var mongosPort int32
	for _, port := range svc.Spec.Ports {
		if port.Name == "mongos" {
			mongosPort = port.Port
			break
		}
	}
	if mongosPort == 0 {
		return "", errors.New("mongos port not found in service")
	}

	mongos := cr.Spec.Sharding.Mongos
	if mongos.Expose.ExposeType == corev1.ServiceTypeLoadBalancer && svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		var host string
		var err error

		if useInternalAddr {
			host, err = getServiceClusterIP(ctx, cl, nn)
		} else {
			host, err = getServiceIngressAddr(ctx, cl, nn)
		}

		if err != nil {
			return "", errors.Wrap(err, "get ingress endpoint")
		}
		return host, nil
	}

	return fmt.Sprintf("%s.%s.%s:%d", svc.Name, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix, mongosPort), nil
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
func GetAddr(cr *api.PerconaServerMongoDB, pod, replset string, port int32) string {
	return strings.Join([]string{pod, cr.Name + "-" + replset, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
		":" + strconv.Itoa(int(port))
}

// GetServiceMeshAddr returns replicaSet pod address in a service mesh
func GetServiceMeshAddr(cr *api.PerconaServerMongoDB, pod string, port int32) string {
	return strings.Join([]string{pod, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
		":" + strconv.Itoa(int(port))
}

// GetMCSAddr returns ReplicaSet pod address using MultiCluster FQDN
func GetMCSAddr(cr *api.PerconaServerMongoDB, pod string, port int32) string {
	return fmt.Sprintf("%s.%s.%s:%d", pod, cr.Namespace, cr.Spec.MultiCluster.DNSSuffix, port)
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
