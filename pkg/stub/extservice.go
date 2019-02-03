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
	"strings"
	"time"
)

func (h *Handler) createSvcs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) error {
	svcsAmount := svcAmount(replset)

	for s := 0; s < int(svcsAmount); s++ {
		svc := svc(m, replset, m.Name+"-"+replset.Name+"-"+fmt.Sprint(s))

		logrus.Debugf("Service %s meta: %v", svc.Name, svc)

		if err := h.client.Create(svc); err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create %s service for replset %s: %v", replset.Name, svc.Name, err)
			}
			logrus.Infof("Service %s already exist, skipping", svc.Name)
			continue
		}
		logrus.Infof("Service %s for replset %s created", svc.Name, replset.Name)
	}
	return nil
}

func (h *Handler) bindSvcs(svcs *corev1.ServiceList, pods *corev1.PodList) error {
	for _, svc := range svcs.Items {

		logrus.Infof("Trying to bind pod to service %s", svc.Name)

		for _, pod := range pods.Items {

			logrus.Debugf("Checking pod %s", pod.Name)

			if strings.Contains(svc.Name, pod.Name) {
				if err := h.attachSvc(&svc, &pod); err != nil {
					return fmt.Errorf("failed to bind pod %s to service %s: %v", pod.Name, svc.Name, err)
				}
			}
		}
	}
	return nil
}

func bindableSvcs(svcs *corev1.ServiceList, pods *corev1.PodList) {
	for _, svc := range svcs.Items {
		for i, pod := range pods.Items {
			if !strings.Contains(svc.Name, pod.Name) {
				pods.Items = append(pods.Items[:i], pods.Items[i+1:]...)
			}
		}
	}
}

func (h *Handler) attachSvc(svc *corev1.Service, pod *corev1.Pod) error {
	logrus.Infof("Trying to attach pod %s to service %s", pod.Name, svc.Name)

	svc.Spec.Selector = map[string]string{"statefulset.kubernetes.io/pod-name": pod.Name}
	svc.Labels["attached"] = "true"

	if err := h.updateSvc(svc); err != nil {
		return fmt.Errorf("failed to attach pod %s to service %s: %v", pod.Name, svc.Name, err)
	}
	return nil
}

func getSvc(m *v1alpha1.PerconaServerMongoDB, podName string) (*corev1.Service, error) {
	var retries uint64 = 0

	client := sdk.NewClient()
	svcMeta := svcMeta(m.Namespace, podName)

	for retries <= 5 {
		if err := client.Get(svcMeta); err != nil {
			if errors.IsNotFound(err) {
				retries += 1
				time.Sleep(500 * time.Millisecond)
				logrus.Infof("Service for %s not found. Retry", podName)
				continue
			}
			return nil, fmt.Errorf("failed to fetch service: %v", err)
		}
		return svcMeta, nil
	}
	return nil, fmt.Errorf("failed to fetch service. Retries limit reached")
}

func (h *Handler) updateSvc(svc *corev1.Service) error {
	var retries uint64 = 0

	for retries <= 5 {
		if err := h.client.Update(svc); err != nil {
			if errors.IsConflict(err) {
				retries += 1
				time.Sleep(500 * time.Millisecond)
				continue
			} else {
				return fmt.Errorf("failed to update service: %v", err)
			}
		}
		return nil
	}
	return fmt.Errorf("failed to update service %s, retries limit reached", svc.Name)
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

func (h *Handler) svcList(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, attached bool) (*corev1.ServiceList, error) {
	svcs := &corev1.ServiceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	lbls := svcLabels(m, replset)
	if attached {
		lbls["attached"] = "true"
	}

	if err := h.client.List(m.Namespace, svcs, opSdk.WithListOptions(&metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(lbls).String()})); err != nil {
		return nil, fmt.Errorf("couldn't fetch services: %v", err)
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
		svc.Spec.ExternalTrafficPolicy = "Local"
		svc.Annotations = map[string]string{"service.beta.kubernetes.io/aws-load-balancer-backend-protocol": "tcp"}

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

func getIngressPoint(svc corev1.Service, pod corev1.Pod) (string, error) {
	var retries uint64 = 0

	client := sdk.NewClient()

	meta := svcMeta(svc.Namespace, pod.Name)

	ticker := time.NewTicker(1 * time.Second)

	for range ticker.C {

		if retries >= 900 {
			ticker.Stop()
			return "", fmt.Errorf("failed to fetch service. Retries limit reached")
		}

		if err := client.Get(meta); err != nil {
			ticker.Stop()
			return "", fmt.Errorf("failed to fetch service: %v", err)
		}

		if len(meta.Status.LoadBalancer.Ingress) != 0 {
			ticker.Stop()
		}
		retries++
	}

	if len(meta.Status.LoadBalancer.Ingress) == 0 {
		return "", fmt.Errorf("cannot detect ingress point for Service %s", meta.Name)
	}

	ip := meta.Status.LoadBalancer.Ingress[0].IP
	hostname := meta.Status.LoadBalancer.Ingress[0].Hostname

	if ip == "" && hostname == "" {
		return "", fmt.Errorf("cannot fetch any hostname from ingress for Service %s", meta.Name)
	}
	if ip != "" {
		return ip, nil
	}
	return hostname, nil
}

func getServiceAddr(svc corev1.Service, pod corev1.Pod) (*ServiceAddr, error) {
	addr := &ServiceAddr{}

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
		host, err := getIngressPoint(svc, pod)
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

func svcAmount(replset *v1alpha1.ReplsetSpec) int {
	svcsAmount := replset.Size

	if replset.Arbiter != nil && replset.Arbiter.Enabled {
		svcsAmount = svcsAmount + replset.Arbiter.Size
	}
	return int(svcsAmount)
}
