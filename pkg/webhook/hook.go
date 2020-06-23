package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"

	kubeErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kubecorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/minio/minio-go"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	admissionv1 "k8s.io/api/admission/v1"
	kubeJson "k8s.io/apimachinery/pkg/runtime/serializer/json"
)

var (
	testData = []byte("{test:1}")
	log      = logf.Log.WithName("webhook")
)

const (
	secretName      = "webhook-certs"
	secretCrtKey    = "cert"
	secretKeyKey    = "key"
	secretBundleKey = "ca-bundle"
	awsKeySecret    = "AWS_ACCESS_KEY_ID"
	awsAccessSecret = "AWS_SECRET_ACCESS_KEY"
)

type Server struct {
	namespace  string
	client     client.Client
	serializer *kubeJson.Serializer
}

func NewServer(mgr manager.Manager, namespace string) (*Server, error) {
	err := admissionv1.AddToScheme(mgr.GetScheme())
	if err != nil {
		return nil, err
	}

	decoder := kubeJson.NewSerializerWithOptions(kubeJson.DefaultMetaFactory, mgr.GetScheme(), mgr.GetScheme(), kubeJson.SerializerOptions{
		Pretty: true,
	})

	return &Server{
		namespace:  namespace,
		serializer: decoder,
		client:     mgr.GetClient(),
	}, nil
}

func (s *Server) NeedLeaderElection() bool {
	return false
}

func (s *Server) Start(stop <-chan struct{}) error {
	mux := http.NewServeMux()
	mux.Handle("/validate", http.HandlerFunc(s.admissionRequestHandler))

	srv := &http.Server{
		Addr:         ":9443",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      mux,
	}

	certPath, keyPath, err := getTLSCertsPath(s.client, s.namespace)
	if err != nil {
		return err
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		<-stop
		log.Info("shutting down webhook server")

		err := srv.Shutdown(context.Background())
		if err != nil {
			log.Error(err, "")
		}

		close(idleConnsClosed)
	}()

	err = srv.ListenAndServeTLS(certPath, keyPath)
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	<-idleConnsClosed
	return nil
}

func (s *Server) admissionRequestHandler(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	timeout := 10 * time.Second
	if t := req.URL.Query().Get("timeout"); t != "" {
		newTimeout, err := strconv.Atoi(t[:len(t)-1])
		if err == nil {
			timeout = time.Duration(newTimeout) * time.Second
		}
	}

	ctx, _ := context.WithDeadline(req.Context(), time.Now().Add(timeout))
	req = req.WithContext(ctx)

	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Error(err, "failed to read request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s.serializer.Decode(data, nil, nil)
	obj, _, err := s.serializer.Decode(data, nil, nil)
	if err != nil {
		log.Error(err, "failed to decode request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	r := obj.(*admissionv1.AdmissionReview)
	r.Response = s.validateStorages(req.Context(), r.Request)
	r.Response.UID = r.Request.UID

	err = s.serializer.Encode(r, w)
	if err != nil {
		log.Error(err, "failed to encode response body")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (s *Server) validateStorages(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	psmdb := &v1.PerconaServerMongoDB{}
	err := json.Unmarshal(req.Object.Raw, psmdb)
	if err != nil {
		log.Error(err, "")
		return nil
	}

	for _, v := range psmdb.Spec.Backup.Storages {
		if v.Type != v1.BackupStorageS3 {
			continue
		}

		err := s.validateS3(ctx, req.Namespace, v.S3)
		if err != nil {
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    http.StatusBadRequest,
					Reason:  metav1.StatusReasonBadRequest,
					Message: "Can't validate s3 storage, err:" + err.Error(),
				},
			}
		}
	}

	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Result: &metav1.Status{
			Status: metav1.StatusSuccess,
			Code:   http.StatusOK,
		},
	}
}

func (s *Server) validateS3(ctx context.Context, namespace string, spec v1.BackupStorageS3Spec) error {
	awsSecret := kubecorev1.Secret{}
	err := s.client.Get(ctx, types.NamespacedName{Name: spec.CredentialsSecret, Namespace: namespace}, &awsSecret)
	if err != nil {
		return err
	}

	keyID, accesKey := string(awsSecret.Data[awsKeySecret]), string(awsSecret.Data[awsAccessSecret])
	if keyID == "" || accesKey == "" {
		return errors.New("storage: S3 credentials are missing")
	}

	if spec.EndpointURL == "" {
		spec.EndpointURL = "https://s3.amazonaws.com"
	}

	useTLS := true
	if strings.HasPrefix(spec.EndpointURL, "http://") {
		useTLS = false
	}

	backupURL, err := url.Parse(spec.EndpointURL)
	if err != nil {
		return err
	}

	cl, err := minio.NewWithRegion(backupURL.Host, keyID, accesKey, useTLS, spec.Region)
	if err != nil {
		return err
	}

	filename := "operator-test-file-" + strconv.Itoa(rand.Int()) + ".json"

	n, err := cl.PutObjectWithContext(ctx, spec.Bucket, filename, bytes.NewReader(testData), int64(len(testData)),
		minio.PutObjectOptions{
			ContentType: minio.SelectObjectTypeJSON,
		},
	)
	if err != nil {
		return err
	}

	if int(n) != len(testData) {
		return errors.New("minio: invalid bytes written")
	}

	// skip this error since it could be not allowed to delete
	cl.RemoveObject(spec.Bucket, filename)
	log.Info("Sucessfully validated storage")

	return nil
}

func getTLSCertsPath(cl client.Client, namespace string) (string, string, error) {
	cert, key, err := readCertFromSecret(cl, namespace)
	if err != nil && !kubeErr.IsNotFound(err) {
		return "", "", err
	}

	if kubeErr.IsNotFound(err) {
		ca, tlsCert, tlsKey, err := tls.Issue([]string{"percona-server-mongodb-operator." + namespace + ".svc"})
		if err != nil {
			return "", "", err
		}

		err = writeBundleToSecret(cl, namespace, tlsCert, tlsKey, ca)
		if err != nil {
			return "", "", err
		}

		cert, key = tlsCert, tlsKey
	}

	return writeCertsToFile(cert, key)
}

func readCertFromSecret(cl client.Client, namespace string) ([]byte, []byte, error) {
	certSecret := kubecorev1.Secret{}
	err := cl.Get(context.Background(), types.NamespacedName{Name: secretName, Namespace: namespace}, &certSecret)
	if err != nil {
		return nil, nil, err
	}
	return certSecret.Data[secretCrtKey], certSecret.Data[secretKeyKey], nil
}

func writeCertsToFile(tlsCert, certKey []byte) (string, string, error) {
	certDir, err := ioutil.TempDir("/tmp", "*")
	if err != nil {
		return "", "", err
	}

	KeyPath, CertPath := certDir+"tls.key", certDir+"tls.crt"

	f, err := os.Create(CertPath)
	if err != nil {
		return "", "", err
	}

	_, err = f.Write(tlsCert)
	if err != nil {
		return "", "", err
	}

	err = f.Close()
	if err != nil {
		return "", "", err
	}

	f, err = os.Create(KeyPath)
	if err != nil {
		return "", "", err
	}

	_, err = f.Write(certKey)
	if err != nil {
		return "", "", err
	}

	err = f.Close()
	if err != nil {
		return "", "", err
	}

	return CertPath, KeyPath, nil
}

func writeBundleToSecret(cl client.Client, namespace string, cert, key, bundle []byte) error {
	secret := &kubecorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			secretCrtKey:    cert,
			secretKeyKey:    key,
			secretBundleKey: bundle,
		},
	}
	return cl.Create(context.Background(), secret)
}
