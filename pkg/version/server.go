package version

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

var log = logf.Log.WithName("version")

type Platform string

const (
	PlatformUndef      Platform = ""
	PlatformKubernetes Platform = "kubernetes"
	PlatformOpenshift  Platform = "openshift"
)

type CloudProvider string

const (
	CloudProviderUndef     CloudProvider = ""
	CloudProviderUnknown   CloudProvider = "unknown"
	CloudProviderGKE       CloudProvider = "gke"
	CloudProviderEKS       CloudProvider = "eks"
	CloudProviderAKS       CloudProvider = "aks"
	CloudProviderDOKS      CloudProvider = "doks"
	CloudProviderOKE       CloudProvider = "oke"
	CloudProviderACK       CloudProvider = "ack"
	CloudProviderNKP       CloudProvider = "nkp"
	CloudProviderPlatform9 CloudProvider = "platform9"
	CloudProviderTanzu     CloudProvider = "tanzu"
	CloudProviderRancher   CloudProvider = "rancher"
)

// ServerVersion represents info about k8s / openshift server version
type ServerVersion struct {
	Platform      Platform
	CloudProvider CloudProvider
	Info          k8sversion.Info
}

// String returns the platform identifier reported to telemetry, with the
// cloud provider appended when detected (e.g. "kubernetes-gke").
func (s *ServerVersion) String() string {
	if s == nil {
		return ""
	}
	if s.CloudProvider == CloudProviderUndef {
		return string(s.Platform)
	}
	return string(s.Platform) + "-" + string(s.CloudProvider)
}

var (
	cVersion *ServerVersion
	mx       sync.Mutex
)

// Server returns server version and platform (k8s|oc)
// it performs API requests for the first invocation and then returns "cached" value
func Server(cl clientcmd.Client) (*ServerVersion, error) {
	mx.Lock()
	defer mx.Unlock()
	if cVersion != nil {
		return cVersion, nil
	}

	v, err := GetServer(cl)
	if err != nil {
		return nil, err
	}

	cVersion = v

	return cVersion, nil
}

// GetServer make request to platform server and returns server version and platform (k8s|oc)
func GetServer(cl clientcmd.Client) (*ServerVersion, error) {
	client := cl.REST()

	version, err := probePlatform(client)
	if err != nil {
		return version, err
	}

	version.CloudProvider = DetectCloudProvider(context.TODO(), client, cl.Config())

	return version, nil
}

func probePlatform(client rest.Interface) (*ServerVersion, error) {
	version := &ServerVersion{}
	// oc 3.9
	var err error
	version.Info, err = probeAPI("/version/openshift", client)
	if err == nil {
		version.Platform = PlatformOpenshift
		return version, nil
	}

	// oc 3.11+
	version.Info, err = probeAPI("/oapi/v1", client)
	if err == nil {
		version.Platform = PlatformOpenshift
		version.Info.GitVersion = "undefined (v3.11+)"
		return version, nil
	}

	// openshift 4.0
	version.Info, err = probeAPI("/apis/quota.openshift.io", client)
	if err == nil {
		version.Platform = PlatformOpenshift
		version.Info.GitVersion = "undefined (v4.0+)"
		return version, nil
	}

	// k8s
	version.Info, err = probeAPI("/version", client)
	if err == nil {
		version.Platform = PlatformKubernetes
		return version, nil
	}

	return version, err
}

type cloudProbe struct {
	provider  CloudProvider
	apiGroups []string
	hosts     []string
	custom    func(ctx context.Context, cfg *rest.Config) bool
}

var cloudProbes = []cloudProbe{
	{provider: CloudProviderGKE, apiGroups: []string{"networking.gke.io", "cloud.google.com"}},
	{provider: CloudProviderEKS, apiGroups: []string{"crd.k8s.amazonaws.com", "metrics.eks.amazonaws.com", "vpcresources.k8s.aws"}},
	{provider: CloudProviderAKS, hosts: []string{".azmk8s.io"}, custom: detectAKS},
	{provider: CloudProviderDOKS, apiGroups: []string{"dataplane-operator.doks.digitalocean.com"}},
	{provider: CloudProviderOKE, apiGroups: []string{"oci.oraclecloud.com"}, hosts: []string{".oraclecloud.com"}},
	{provider: CloudProviderACK, apiGroups: []string{"alibabacloud.com"}, hosts: []string{".aliyuncs.com"}},
	{provider: CloudProviderNKP, apiGroups: []string{"nkp.nutanix.com", "kommander.mesosphere.io"}},
	{provider: CloudProviderPlatform9, hosts: []string{".platform9.io", ".platform9.net"}},
	{provider: CloudProviderTanzu, apiGroups: []string{"run.tanzu.vmware.com"}},
	{provider: CloudProviderRancher, apiGroups: []string{"management.cattle.io"}},
}

// DetectCloudProvider returns the managed Kubernetes offering the operator runs
// on, or CloudProviderUnknown when no probe matches.
func DetectCloudProvider(ctx context.Context, client rest.Interface, cfg *rest.Config) CloudProvider {
	groups, err := serverGroups(ctx, client)
	if err != nil {
		log.V(1).Info("failed to list API groups", "error", err.Error())
	}

	host := ""
	if cfg != nil {
		host = cfg.Host
	}

	provider := matchCloudProvider(ctx, groups, host, cfg)
	if provider == CloudProviderUnknown {
		log.V(1).Info("cloud provider not detected")
	}

	return provider
}

func matchCloudProvider(ctx context.Context, groups map[string]struct{}, host string, cfg *rest.Config) CloudProvider {
	for _, p := range cloudProbes {
		for _, group := range p.apiGroups {
			if _, ok := groups[group]; ok {
				log.Info("cloud provider detected", "provider", p.provider, "signal", "apigroup:"+group)
				return p.provider
			}
		}
		for _, h := range p.hosts {
			if strings.Contains(host, h) {
				log.Info("cloud provider detected", "provider", p.provider, "signal", "host:"+h)
				return p.provider
			}
		}
		if p.custom != nil && p.custom(ctx, cfg) {
			log.Info("cloud provider detected", "provider", p.provider, "signal", "custom")
			return p.provider
		}
	}

	return CloudProviderUnknown
}

func serverGroups(ctx context.Context, client rest.Interface) (map[string]struct{}, error) {
	body, err := client.Get().AbsPath("/apis").Do(ctx).Raw()
	if err != nil {
		return nil, err
	}

	var list metav1.APIGroupList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}

	groups := make(map[string]struct{}, len(list.Groups))
	for _, g := range list.Groups {
		groups[g.Name] = struct{}{}
	}

	return groups, nil
}

func detectAKS(ctx context.Context, cfg *rest.Config) bool {
	if cfg == nil {
		return false
	}

	tlsCfg, err := rest.TLSConfigFor(cfg)
	if err != nil {
		log.V(1).Info("failed to build TLS config", "error", err.Error())
		return false
	}

	host := strings.TrimPrefix(cfg.Host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	netConn, err := (&tls.Dialer{Config: tlsCfg}).DialContext(dialCtx, "tcp", host)
	if err != nil {
		log.V(1).Info("failed to dial API server", "error", err.Error())
		return false
	}
	defer netConn.Close() //nolint:errcheck

	conn, ok := netConn.(*tls.Conn)
	if !ok {
		return false
	}

	for _, cert := range conn.ConnectionState().PeerCertificates {
		for _, san := range cert.DNSNames {
			if strings.HasSuffix(san, ".azmk8s.io") {
				return true
			}
		}
	}

	return false
}

func probeAPI(path string, client rest.Interface) (k8sversion.Info, error) {
	var vInfo k8sversion.Info
	vBody, err := client.Get().AbsPath(path).Do(context.TODO()).Raw()
	if err != nil {
		return vInfo, err
	}

	err = json.Unmarshal(vBody, &vInfo)
	if err != nil {
		return vInfo, err
	}

	return vInfo, nil
}
