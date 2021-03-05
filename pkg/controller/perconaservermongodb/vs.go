package perconaservermongodb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/percona/percona-server-mongodb-operator/versionserviceclient"
	"github.com/percona/percona-server-mongodb-operator/versionserviceclient/models"
	"github.com/percona/percona-server-mongodb-operator/versionserviceclient/version_service"
)

const productName = "psmdb-operator"

func (vs VersionServiceClient) GetExactVersion(endpoint string, vm VersionMeta) (DepVersion, error) {
	if strings.Contains(endpoint, "https://check.percona.com/versions") {
		endpoint = "https://check.percona.com"
	}

	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return DepVersion{}, err
	}

	vsClient := versionserviceclient.NewHTTPClientWithConfig(nil, &versionserviceclient.TransportConfig{
		Host:     requestURL.Host,
		BasePath: requestURL.Path,
		Schemes:  []string{requestURL.Scheme},
	})

	applyParams := &version_service.VersionServiceApplyParams{
		Apply:             vm.Apply,
		BackupVersion:     &vm.BackupVersion,
		CustomResourceUID: &vm.CRUID,
		DatabaseVersion:   &vm.MongoVersion,
		KubeVersion:       &vm.KubeVersion,
		OperatorVersion:   vm.Version,
		Platform:          &vm.Platform,
		PmmVersion:        &vm.PMMVersion,
		Product:           productName,
		HTTPClient:        &http.Client{Timeout: 10 * time.Second},
	}
	applyParams = applyParams.WithTimeout(10 * time.Second)

	resp, err := vsClient.VersionService.VersionServiceApply(applyParams)

	if err != nil {
		return DepVersion{}, err
	}

	if len(resp.Payload.Versions) == 0 {
		return DepVersion{}, fmt.Errorf("empty versions response")
	}

	mongoVersion, err := getVersion(resp.Payload.Versions[0].Matrix.Mongod)
	if err != nil {
		return DepVersion{}, err
	}

	backupVersion, err := getVersion(resp.Payload.Versions[0].Matrix.Backup)
	if err != nil {
		return DepVersion{}, err
	}

	pmmVersion, err := getVersion(resp.Payload.Versions[0].Matrix.Pmm)
	if err != nil {
		return DepVersion{}, err
	}

	return DepVersion{
		MongoImage:    resp.Payload.Versions[0].Matrix.Mongod[mongoVersion].ImagePath,
		MongoVersion:  mongoVersion,
		BackupImage:   resp.Payload.Versions[0].Matrix.Backup[backupVersion].ImagePath,
		BackupVersion: backupVersion,
		PMMImage:      resp.Payload.Versions[0].Matrix.Pmm[pmmVersion].ImagePath,
		PMMVersion:    pmmVersion,
	}, nil
}

func getVersion(versions map[string]models.VersionVersion) (string, error) {
	if len(versions) != 1 {
		return "", fmt.Errorf("response has multiple or zero versions")
	}

	for k := range versions {
		return k, nil
	}
	return "", nil
}

type DepVersion struct {
	MongoImage    string `json:"mongoImage,omitempty"`
	MongoVersion  string `json:"mongoVersion,omitempty"`
	BackupImage   string `json:"backupImage,omitempty"`
	BackupVersion string `json:"backupVersion,omitempty"`
	PMMImage      string `json:"pmmImage,omitempty"`
	PMMVersion    string `json:"pmmVersion,omitempty"`
}

type VersionService interface {
	GetExactVersion(endpoint string, vm VersionMeta) (DepVersion, error)
}

type VersionServiceClient struct{}

type Version struct {
	Version   string `json:"version"`
	ImagePath string `json:"imagePath"`
	Imagehash string `json:"imageHash"`
	Status    string `json:"status"`
	Critilal  bool   `json:"critilal"`
}

type VersionMatrix struct {
	Mongo  map[string]Version `json:"mongod"`
	PMM    map[string]Version `json:"pmm"`
	Backup map[string]Version `json:"backup"`
}

type OperatorVersion struct {
	Operator string        `json:"operator"`
	Database string        `json:"database"`
	Matrix   VersionMatrix `json:"matrix"`
}

type VersionResponse struct {
	Versions []OperatorVersion `json:"versions"`
}

type VersionMeta struct {
	Apply         string
	MongoVersion  string
	KubeVersion   string
	Platform      string
	PMMVersion    string
	BackupVersion string
	CRUID         string
	Version       string
}
