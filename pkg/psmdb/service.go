package psmdb

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
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

	return &corev1.Service{
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
					Port:       m.Spec.Mongod.Net.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}
}

// ExternalService returns a Service object needs to serve external connections
func ExternalService(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podName string) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.Namespace,
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
				Port:       m.Spec.Mongod.Net.Port,
				TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
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

func GetServiceAddr(svc corev1.Service, pod corev1.Pod, cl client.Client) (*ServiceAddr, error) {
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
		host, err := getIngressPoint(pod, cl)
		if err != nil {
			return nil, err
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

func getIngressPoint(pod corev1.Pod, cl client.Client) (string, error) {
	var retries uint64 = 0

	var ip string
	var hostname string

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		retries++

		if retries >= 1000 {
			return "", fmt.Errorf("failed to fetch service. Retries limit reached")
		}

		svc := &corev1.Service{}
		err := cl.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, svc)
		if err != nil {
			return "", fmt.Errorf("failed to fetch service: %v", err)
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
	return "", fmt.Errorf("can't get service %s ingress, retry limit reached", pod.Name)
}
