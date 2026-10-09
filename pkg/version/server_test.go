package version

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	stdlog "log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

type fakeCmdClient struct {
	rest rest.Interface
	cfg  *rest.Config
}

var _ clientcmd.Client = (*fakeCmdClient)(nil)

func (f *fakeCmdClient) Exec(context.Context, *corev1.Pod, string, []string, io.Reader, io.Writer, io.Writer, bool) error {
	return nil
}

func (f *fakeCmdClient) REST() rest.Interface { return f.rest }

func (f *fakeCmdClient) Config() *rest.Config { return f.cfg }

func groupSet(groups ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		s[g] = struct{}{}
	}
	return s
}

func restClientFor(t *testing.T, cfg *rest.Config) rest.Interface {
	t.Helper()

	cfg.ContentConfig = rest.ContentConfig{
		GroupVersion:         &metav1.SchemeGroupVersion,
		NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
	}
	cl, err := rest.RESTClientFor(cfg)
	require.NoError(t, err)

	return cl
}

// newTLSServer starts an HTTPS test server presenting a self-signed
// certificate with the given SANs and returns its rest.Config.
func newTLSServer(t *testing.T, sans ...string) *rest.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		DNSNames:     sans,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.Config.ErrorLog = stdlog.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return &rest.Config{Host: srv.URL, Insecure: true}
}

func writeGroups(t *testing.T, w http.ResponseWriter, names ...string) {
	t.Helper()

	list := metav1.APIGroupList{}
	for _, n := range names {
		list.Groups = append(list.Groups, metav1.APIGroup{Name: n})
	}
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(list))
}

func TestServerVersionString(t *testing.T) {
	tests := map[string]struct {
		version *ServerVersion
		want    string
	}{
		"nil version":            {version: nil, want: ""},
		"empty":                  {version: &ServerVersion{}, want: ""},
		"kubernetes no provider": {version: &ServerVersion{Platform: PlatformKubernetes}, want: "kubernetes"},
		"kubernetes with gke": {
			version: &ServerVersion{Platform: PlatformKubernetes, CloudProvider: CloudProviderGKE},
			want:    "kubernetes-gke",
		},
		"openshift with aks": {
			version: &ServerVersion{Platform: PlatformOpenshift, CloudProvider: CloudProviderAKS},
			want:    "openshift-aks",
		},
		"provider without platform": {
			version: &ServerVersion{CloudProvider: CloudProviderEKS},
			want:    "-eks",
		},
		"undetected provider": {
			version: &ServerVersion{Platform: PlatformKubernetes, CloudProvider: CloudProviderUnknown},
			want:    "kubernetes-unknown",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.version.String())
		})
	}
}

func TestMatchCloudProvider(t *testing.T) {
	tests := map[string]struct {
		groups map[string]struct{}
		host   string
		setup  func(t *testing.T) *rest.Config
		want   CloudProvider
	}{
		"no signals":              {want: CloudProviderUnknown},
		"nil groups unknown host": {groups: nil, host: "https://10.0.0.1:6443", want: CloudProviderUnknown},
		"gke networking group":    {groups: groupSet("networking.gke.io"), want: CloudProviderGKE},
		"gke legacy group":        {groups: groupSet("cloud.google.com"), want: CloudProviderGKE},
		"eks vpc cni group":       {groups: groupSet("crd.k8s.amazonaws.com"), want: CloudProviderEKS},
		"eks metrics group":       {groups: groupSet("metrics.eks.amazonaws.com"), want: CloudProviderEKS},
		"eks vpcresources group":  {groups: groupSet("vpcresources.k8s.aws"), want: CloudProviderEKS},
		"aks host":                {host: "https://test-dns.hcp.westeurope.azmk8s.io:443", want: CloudProviderAKS},
		"aks private dns, cert san": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t, "private.hcp.eastus.azmk8s.io") },
			want:  CloudProviderAKS,
		},
		"non-aks cert san": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t, "kubernetes.default.svc") },
			want:  CloudProviderUnknown,
		},
		"doks group":              {groups: groupSet("dataplane-operator.doks.digitalocean.com"), want: CloudProviderDOKS},
		"oke group":               {groups: groupSet("oci.oraclecloud.com"), want: CloudProviderOKE},
		"oke host":                {host: "https://abc.kubernetes.eu-frankfurt-1.oraclecloud.com:6443", want: CloudProviderOKE},
		"ack group":               {groups: groupSet("alibabacloud.com"), want: CloudProviderACK},
		"ack host":                {host: "https://abc.eu-central-1.aliyuncs.com:6443", want: CloudProviderACK},
		"nkp group":               {groups: groupSet("nkp.nutanix.com"), want: CloudProviderNKP},
		"nkp legacy konvoy group": {groups: groupSet("kommander.mesosphere.io"), want: CloudProviderNKP},
		"platform9 io host":       {host: "https://abc.platform9.io", want: CloudProviderPlatform9},
		"platform9 net host":      {host: "https://abc.platform9.net", want: CloudProviderPlatform9},
		"tanzu group":             {groups: groupSet("run.tanzu.vmware.com"), want: CloudProviderTanzu},
		"rancher group":           {groups: groupSet("management.cattle.io"), want: CloudProviderRancher},
		"vanilla cluster": {
			groups: groupSet("apps", "batch", "rbac.authorization.k8s.io"),
			host:   "https://kubernetes.default.svc",
			want:   CloudProviderUnknown,
		},
		"gke wins over rancher": {
			groups: groupSet("management.cattle.io", "networking.gke.io"),
			want:   CloudProviderGKE,
		},
		"earlier probe wins: platform9 host over tanzu group": {
			groups: groupSet("run.tanzu.vmware.com"),
			host:   "https://abc.platform9.io",
			want:   CloudProviderPlatform9,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var cfg *rest.Config
			host := tc.host
			if tc.setup != nil {
				cfg = tc.setup(t)
				host = cfg.Host
			}

			assert.Equal(t, tc.want, matchCloudProvider(t.Context(), tc.groups, host, cfg))
		})
	}
}

func TestDetectCloudProvider(t *testing.T) {
	tests := map[string]struct {
		handler http.HandlerFunc
		host    string
		want    CloudProvider
	}{
		"provider from api groups": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeGroups(t, w, "apps", "dataplane-operator.doks.digitalocean.com")
			},
			want: CloudProviderDOKS,
		},
		"no provider": {
			handler: func(w http.ResponseWriter, r *http.Request) { writeGroups(t, w, "apps") },
			want:    CloudProviderUnknown,
		},
		"api groups unavailable, host still matches": {
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) },
			host:    "https://abc.platform9.io",
			want:    CloudProviderPlatform9,
		},
		"api groups unavailable, no host match": {
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) },
			want:    CloudProviderUnknown,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			cfg := &rest.Config{Host: srv.URL}
			cl := restClientFor(t, &rest.Config{Host: srv.URL})
			if tc.host != "" {
				cfg.Host = tc.host
			}

			assert.Equal(t, tc.want, DetectCloudProvider(t.Context(), cl, cfg))
		})
	}

	t.Run("nil config", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeGroups(t, w, "networking.gke.io")
		}))
		t.Cleanup(srv.Close)

		cl := restClientFor(t, &rest.Config{Host: srv.URL})
		assert.Equal(t, CloudProviderGKE, DetectCloudProvider(t.Context(), cl, nil))
	})
}

func TestGetServer(t *testing.T) {
	tests := map[string]struct {
		handler      http.HandlerFunc
		wantPlatform Platform
		wantProvider CloudProvider
		wantString   string
		wantErr      bool
	}{
		"kubernetes on gke": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/version":
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(k8sversion.Info{GitVersion: "v1.31.0"}))
				case "/apis":
					writeGroups(t, w, "apps", "networking.gke.io")
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantPlatform: PlatformKubernetes,
			wantProvider: CloudProviderGKE,
			wantString:   "kubernetes-gke",
		},
		"openshift 4 on eks": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/apis/quota.openshift.io":
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(k8sversion.Info{}))
				case "/apis":
					writeGroups(t, w, "vpcresources.k8s.aws")
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantPlatform: PlatformOpenshift,
			wantProvider: CloudProviderEKS,
			wantString:   "openshift-eks",
		},
		"undetected provider": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/version":
					w.Header().Set("Content-Type", "application/json")
					assert.NoError(t, json.NewEncoder(w).Encode(k8sversion.Info{GitVersion: "v1.31.0"}))
				case "/apis":
					writeGroups(t, w, "apps")
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantPlatform: PlatformKubernetes,
			wantProvider: CloudProviderUnknown,
			wantString:   "kubernetes-unknown",
		},
		"no platform": {
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			cfg := &rest.Config{Host: srv.URL}
			cl := &fakeCmdClient{rest: restClientFor(t, &rest.Config{Host: srv.URL}), cfg: cfg}

			sv, err := GetServer(cl)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPlatform, sv.Platform)
			assert.Equal(t, tc.wantProvider, sv.CloudProvider)
			assert.Equal(t, tc.wantString, sv.String())
		})
	}
}

func TestServerGroups(t *testing.T) {
	tests := map[string]struct {
		handler    http.HandlerFunc
		want       map[string]struct{}
		wantErrMsg string
	}{
		"groups returned": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/apis", r.URL.Path)
				writeGroups(t, w, "apps", "networking.gke.io")
			},
			want: groupSet("apps", "networking.gke.io"),
		},
		"empty list": {
			handler: func(w http.ResponseWriter, r *http.Request) { writeGroups(t, w) },
			want:    map[string]struct{}{},
		},
		"server error": {
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			wantErrMsg: "an error on the server",
		},
		"malformed body": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte("not json"))
				assert.NoError(t, err)
			},
			wantErrMsg: "invalid character",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			got, err := serverGroups(t.Context(), restClientFor(t, &rest.Config{Host: srv.URL}))
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDetectAKS(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T) *rest.Config
		want  bool
	}{
		"nil config": {
			setup: func(t *testing.T) *rest.Config { return nil },
		},
		"matching cert san": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t, "abc.hcp.eastus.azmk8s.io") },
			want:  true,
		},
		"matching san among several": {
			setup: func(t *testing.T) *rest.Config {
				return newTLSServer(t, "kubernetes", "abc.hcp.eastus.azmk8s.io")
			},
			want: true,
		},
		"no san": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t) },
		},
		"non-matching san": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t, "kubernetes.default.svc") },
		},
		"lookalike san suffix": {
			setup: func(t *testing.T) *rest.Config { return newTLSServer(t, "azmk8s.io.example.com") },
		},
		"host without port": {
			setup: func(t *testing.T) *rest.Config {
				return &rest.Config{Host: "https://127.0.0.1", Insecure: true}
			},
		},
		"unreachable host": {
			setup: func(t *testing.T) *rest.Config { return &rest.Config{Host: "https://127.0.0.1:1"} },
		},
		"unreadable ca file": {
			setup: func(t *testing.T) *rest.Config {
				return &rest.Config{Host: "https://127.0.0.1:1", CAFile: "/nonexistent"}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, detectAKS(t.Context(), tc.setup(t)))
		})
	}
}

func TestProbeAPI(t *testing.T) {
	tests := map[string]struct {
		handler    http.HandlerFunc
		want       k8sversion.Info
		wantErrMsg string
	}{
		"version returned": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(k8sversion.Info{Major: "1", Minor: "31", GitVersion: "v1.31.0"}))
			},
			want: k8sversion.Info{Major: "1", Minor: "31", GitVersion: "v1.31.0"},
		},
		"not found": {
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
			wantErrMsg: "the server could not find the requested resource",
		},
		"malformed body": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("<html>"))
				assert.NoError(t, err)
			},
			wantErrMsg: "invalid character",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			got, err := probeAPI("/version", restClientFor(t, &rest.Config{Host: srv.URL}))
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				assert.Equal(t, k8sversion.Info{}, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
