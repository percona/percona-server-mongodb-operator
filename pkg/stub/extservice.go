package stub

import (
	"fmt"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strconv"
	"time"
)

func (h *Handler) createSvcs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) error {
	setExposeDefaults(replset)

	for r := 0; r < int(replset.Size); r++ {
		replica := svc(m, replset, m.Name+"-"+replset.Name+"-"+fmt.Sprint(r))
		replica.Spec.Selector = map[string]string{"statefulset.kubernetes.io/pod-name": m.Name + "-" + replset.Name + "-" + fmt.Sprint(r)}

		if err := h.client.Create(replica); err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create %s service for replset %s: %v", replset.Name, replica.Name, err)
			}
			logrus.Infof("Service %s already exist, skipping", replica.Name)
			continue
		}
		logrus.Infof("Service %s for replset %s created", replica.Name, replset.Name)
	}

	if replset.Arbiter != nil && replset.Arbiter.Enabled {
		for r := 0; r < int(replset.Arbiter.Size); r++ {
			replica := svc(m, replset, m.Name+"-"+replset.Name+"-arbiter-"+fmt.Sprint(r))
			replica.Spec.Selector = map[string]string{"statefulset.kubernetes.io/pod-name": m.Name + "-" + replset.Name + "-arbiter-" + fmt.Sprint(r)}

			if err := h.client.Create(replica); err != nil {
				if !errors.IsAlreadyExists(err) {
					return fmt.Errorf("failed to create %s service for replset arbiter %s: %v", replset.Name, replica.Name, err)
				}
				logrus.Infof("Service %s already exist, skipping", replica.Name)
				continue
			}
			logrus.Infof("Service %s for replset arbiter %s created", replica.Name, replset.Name)
		}
	}
	return nil
}

func getSvc(m *v1alpha1.PerconaServerMongoDB, podName string) (*corev1.Service, error) {
	logrus.Infof("Fetching service that attached to pod %s", podName)

	client := sdk.NewClient()
	svc := svcMeta(m.Namespace, podName)

	if err := client.Get(svc); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("service %s not found: %v", podName, err)
		}
		return nil, fmt.Errorf("failed to fetch service %s: %v", podName, err)
	}
	return svc, nil
}

func svcMeta(namespace, name string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func (h *Handler) svcList(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) (*corev1.ServiceList, error) {
	svcs := &corev1.ServiceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}
	lbls := svcLabels(m, replset)

	if err := h.client.List(m.Namespace, svcs, opSdk.WithListOptions(&metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(lbls).String()})); err != nil {
		return nil, fmt.Errorf("couldn't fetch services: %v", err)
	}

	if replset.Expose != nil && replset.Expose.Enabled && replset.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
		ingressSvcs := make([]corev1.Service, len(svcs.Items))

		for _, svc := range svcs.Items {
			if len(svc.Status.LoadBalancer.Ingress) > 0 {
				ingressSvcs = append(ingressSvcs, svc)
			}
		}
		svcs.Items = ingressSvcs
	}

	return svcs, nil
}

func svc(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, name string) *corev1.Service {
	svc := svcMeta(m.Namespace, name)
	svc.Labels = svcLabels(m, replset)
	svc.Spec = corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongod.MongodPortName,
				Port:       m.Spec.Mongod.Net.Port,
				TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
			},
		},
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

	util.AddOwnerRefToObject(svc, util.AsOwner(m))

	return svc
}

func svcLabels(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) map[string]string {
	return map[string]string{
		"app":     "percona-server-mongodb",
		"replset": replset.Name,
		"cluster": m.Name,
	}
}

type ServiceAddr struct {
	Host string
	Port int
}

func (s ServiceAddr) String() string {
	return s.Host + ":" + strconv.Itoa(s.Port)
}

func setExposeDefaults(replset *v1alpha1.ReplsetSpec) {
	if replset.Expose == nil {
		replset.Expose = &v1alpha1.Expose{
			Enabled: false,
		}
	}
	if replset.Expose.Enabled && replset.Expose.ExposeType == "" {
		replset.Expose.ExposeType = corev1.ServiceTypeClusterIP
	}
}

func getSvcAddr(m *v1alpha1.PerconaServerMongoDB, pod corev1.Pod) (*ServiceAddr, error) {
	logrus.Infof("Fetching service address for pod %s", pod.Name)

	addr := &ServiceAddr{}

	svc, err := getSvc(m, pod.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get service address: %v", err)
	}

	switch svc.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		addr.Host = svc.Spec.ClusterIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongod.MongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeLoadBalancer:
		host, err := getIngressPoint(m, pod)
		if err != nil {
			return nil, err
		}
		addr.Host = host
		for _, p := range svc.Spec.Ports {
			if p.Name != mongod.MongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeNodePort:
		addr.Host = pod.Status.HostIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongod.MongodPortName {
				continue
			}
			addr.Port = int(p.NodePort)
		}
	}
	return addr, nil
}

func getIngressPoint(m *v1alpha1.PerconaServerMongoDB, pod corev1.Pod) (string, error) {
	logrus.Infof("Fetching ingress point for pod %s", pod.Name)

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

		svc, err := getSvc(m, pod.Name)
		if err != nil {
			return "", fmt.Errorf("failed to fetch service: %v", err)
		}
		logrus.Debugf("Service %s:", svc)
		logrus.Debugf("Service %s ingress length: %d", svc.Name, len(svc.Status.LoadBalancer.Ingress))

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

		logrus.Infof("Waiting for %s service ingress", svc.Name)
	}
	return "", fmt.Errorf("can't get service %s ingress, retry limit reached", pod.Name)
}
