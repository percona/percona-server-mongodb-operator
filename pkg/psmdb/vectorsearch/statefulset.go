package vectorsearch

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	psmdbInit "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/init"
)

// secretFileMode is the default mode for Secret projections
const secretFileMode int32 = 0400

// StatefulSet returns the StatefulSet object that runs the mongot
// group for the given replset. configHash is set as a pod-template
// annotation so a ConfigMap content change rolls the pods.
func StatefulSet(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, initImage, configHash string) *appsv1.StatefulSet {
	objectLabels := naming.SearchLabels(cr, rs)
	spec := getSearchSpec(cr, rs)

	podLabels := make(map[string]string, len(objectLabels)+len(spec.Labels))
	for k, v := range objectLabels {
		podLabels[k] = v
	}
	for k, v := range spec.Labels {
		if _, ok := podLabels[k]; !ok {
			podLabels[k] = v
		}
	}

	annotations := spec.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	if configHash != "" {
		annotations["percona.com/configuration-hash"] = configHash
	}

	volumes, claimTemplates := podVolumes(cr, rs, spec)

	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.SearchStatefulSetName(cr, rs),
			Namespace: cr.Namespace,
			Labels:    objectLabels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: naming.SearchServiceName(cr, rs),
			Replicas:    &spec.Size,
			Selector: &metav1.LabelSelector{
				MatchLabels: objectLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					SecurityContext:  spec.PodSecurityContext,
					Affinity:         podAffinity(spec.Affinity, podLabels),
					NodeSelector:     spec.NodeSelector,
					Tolerations:      spec.Tolerations,
					ImagePullSecrets: cr.Spec.ImagePullSecrets,
					InitContainers:   psmdbInit.Containers(cr, initImage),
					Containers:       []corev1.Container{mongotContainer(cr, spec)},
					Volumes:          volumes,
					RestartPolicy:    corev1.RestartPolicyAlways,
					SchedulerName:    cr.Spec.SchedulerName,
				},
			},
			VolumeClaimTemplates: claimTemplates,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
		},
	}
}

func getSearchSpec(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) *api.SearchSpec {
	if cr.Spec.Search == nil {
		return &api.SearchSpec{Enabled: false}
	}

	if rs == nil || rs.Search == nil {
		return cr.Spec.Search
	}

	// Deep-copy before applying overrides so per-replset values don't leak
	// into cr.Spec.Search and get picked up by other replsets.
	spec := cr.Spec.Search.DeepCopy()

	overrides := rs.Search
	if overrides.Size != nil {
		spec.Size = *overrides.Size
	}
	if overrides.Storage != nil {
		spec.Storage = overrides.Storage
	}
	if overrides.Resources != nil {
		spec.Resources = *overrides.Resources
	}
	if overrides.JVMFlags != nil {
		spec.JVMFlags = overrides.JVMFlags
	}
	if overrides.Affinity != nil {
		spec.Affinity = overrides.Affinity
	}
	if overrides.NodeSelector != nil {
		spec.NodeSelector = overrides.NodeSelector
	}
	if overrides.Tolerations != nil {
		spec.Tolerations = overrides.Tolerations
	}
	if overrides.Annotations != nil {
		spec.Annotations = overrides.Annotations
	}
	if overrides.Labels != nil {
		spec.Labels = overrides.Labels
	}
	if overrides.ContainerSecurityContext != nil {
		spec.ContainerSecurityContext = overrides.ContainerSecurityContext
	}
	if overrides.PodSecurityContext != nil {
		spec.PodSecurityContext = overrides.PodSecurityContext
	}

	return spec
}

func jvmFlags(spec *api.SearchSpec) []string {
	flags := []string{}

	var heapConfigured bool
	for _, jvmFlag := range spec.JVMFlags {
		if strings.HasPrefix(jvmFlag, "-Xms") || strings.HasPrefix(jvmFlag, "-Xmx") {
			heapConfigured = true
			break
		}
	}

	var memory *resource.Quantity
	if !spec.Resources.Limits.Memory().IsZero() {
		memory = spec.Resources.Limits.Memory()
	}
	if !spec.Resources.Requests.Memory().IsZero() {
		memory = spec.Resources.Requests.Memory()
	}

	// it's recommended to set the minimum heap size (-Xms) and maximum heap size (-Xmx) to the same value
	// but if any of them are provided by the users we are not setting defaults. Only set defaults if
	// none of them are provided.
	if !heapConfigured && memory != nil && !memory.IsZero() {
		// MongoDB recommends to set the half of memory to the JVM heap
		// https://www.mongodb.com/docs/manual/tutorial/mongot-sizing/advanced-guidance/hardware/#jvm-heap-sizing
		// so we should do that even if the jvm flags are not configured by users.
		halfBytes := memory.Value() / 2
		halfMB := halfBytes / (1024 * 1024)
		flags = append(flags, fmt.Sprintf("-Xmx%dm", halfMB))
		flags = append(flags, fmt.Sprintf("-Xms%dm", halfMB))
	}

	return append(flags, spec.JVMFlags...)
}

func mongotContainer(cr *api.PerconaServerMongoDB, search *api.SearchSpec) corev1.Container {
	mounts := []corev1.VolumeMount{
		{
			Name:      config.BinVolumeName,
			MountPath: config.BinMountPath,
		},
		{
			Name:      DataVolumeName,
			MountPath: DataMountPath,
		},
		{
			Name:      cr.Spec.Secrets.GetInternalKey(cr),
			MountPath: config.MongodSecretsDir,
			ReadOnly:  true,
		},
		{
			Name:      ConfigVolumeName,
			MountPath: ConfigMountPath,
			ReadOnly:  true,
		},
		{
			Name:      UsersSecretVolumeName,
			MountPath: UsersSecretMountPath,
			ReadOnly:  true,
		},
	}

	if cr.TLSEnabled() {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "ssl",
			MountPath: config.SSLDir,
			ReadOnly:  true,
		})
	}

	mongotCmd := []string{
		"mongot-community/mongot",
		"--config=" + ConfigMountPath + "/" + ConfigFileName,
	}

	if flags := jvmFlags(search); len(flags) > 0 {
		mongotCmd = append(mongotCmd, "--jvm-flags")
		mongotCmd = append(mongotCmd, strings.Join(flags, " "))
	}

	return corev1.Container{
		Name:            naming.ContainerMongot,
		Image:           search.Image,
		ImagePullPolicy: search.ImagePullPolicy,
		Ports: []corev1.ContainerPort{
			{
				Name:          GRPCPortName,
				ContainerPort: GRPCPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Command:         []string{"/opt/percona/mongot-entrypoint.sh"},
		Args:            mongotCmd,
		Resources:       search.Resources,
		SecurityContext: search.ContainerSecurityContext,
		VolumeMounts:    mounts,
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt32(HealthCheckPort),
				},
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt32(HealthCheckPort),
				},
			},
		},
	}
}

// podVolumes returns the pod-level Volumes and the StatefulSet's
// VolumeClaimTemplates. The mongot data PVC is templated when the
// effective storage spec points at a PersistentVolumeClaim; otherwise
// it falls back to the inline EmptyDir / HostPath source, matching how
// pkg/psmdb handles mongod's data volume.
func podVolumes(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, search *api.SearchSpec) ([]corev1.Volume, []corev1.PersistentVolumeClaim) {
	fvar := false

	volumes := []corev1.Volume{
		{
			Name: config.BinVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: cr.Spec.Secrets.GetInternalKey(cr),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.GetInternalKey(cr),
					DefaultMode: new(secretFileMode),
					Optional:    &fvar,
				},
			},
		},
		{
			Name: ConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: naming.SearchConfigMapName(cr, rs),
					},
				},
			},
		},
		{
			Name: UsersSecretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  api.InternalUserSecretName(cr),
					DefaultMode: new(secretFileMode),
				},
			},
		},
	}

	if cr.TLSEnabled() {
		volumes = append(volumes, corev1.Volume{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  api.SSLInternalSecretName(cr),
					DefaultMode: new(secretFileMode),
					Optional:    &cr.Spec.Unsafe.TLS,
				},
			},
		})
	}

	var claims []corev1.PersistentVolumeClaim
	switch {
	case search.Storage != nil && search.Storage.PersistentVolumeClaim.PersistentVolumeClaimSpec != nil:
		claims = []corev1.PersistentVolumeClaim{
			persistentVolumeClaim(DataVolumeName, cr.Namespace, search.Storage),
		}
	case search.Storage != nil && (search.Storage.HostPath != nil || search.Storage.EmptyDir != nil):
		volumes = append(volumes, corev1.Volume{
			Name: DataVolumeName,
			VolumeSource: corev1.VolumeSource{
				HostPath: search.Storage.HostPath,
				EmptyDir: search.Storage.EmptyDir,
			},
		})
	default:
		volumes = append(volumes, corev1.Volume{
			Name: DataVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	return volumes, claims
}

func persistentVolumeClaim(name, namespace string, spec *api.VolumeSpec) corev1.PersistentVolumeClaim {
	pvc := corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if spec.PersistentVolumeClaim.PersistentVolumeClaimSpec != nil {
		pvc.Spec = *spec.PersistentVolumeClaim.PersistentVolumeClaimSpec
	}
	return pvc
}

// podAffinity converts an api.PodAffinity into a corev1.Affinity. It
// mirrors psmdb.PodAffinity but lives in this package to avoid an
// import cycle through pkg/psmdb (which already imports this package
// indirectly via the reconciler in a later change).
func podAffinity(af *api.PodAffinity, labels map[string]string) *corev1.Affinity {
	if af == nil {
		return nil
	}

	switch {
	case af.Advanced != nil:
		return af.Advanced
	case af.TopologyKey != nil:
		if *af.TopologyKey == api.AffinityOff {
			return nil
		}
		labelsCopy := make(map[string]string, len(labels))
		for k, v := range labels {
			labelsCopy[k] = v
		}
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labelsCopy,
						},
						TopologyKey: *af.TopologyKey,
					},
				},
			},
		}
	}

	return nil
}
