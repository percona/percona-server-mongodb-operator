package perconaservermongodb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/versionserviceclient"
	"github.com/percona/percona-server-mongodb-operator/versionserviceclient/models"
	"github.com/percona/percona-server-mongodb-operator/versionserviceclient/version_service"
)

const productName = "psmdb-operator"

func (vs VersionServiceClient) GetExactVersion(cr *api.PerconaServerMongoDB, endpoint string, vm VersionMeta, opts versionOptions) (DepVersion, error) {
	if strings.Contains(endpoint, "https://check.percona.com/versions") {
		endpoint = api.GetDefaultVersionServiceEndpoint()
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
		HTTPClient:              &http.Client{Timeout: 10 * time.Second},
		Apply:                   vm.Apply,
		BackupVersion:           &vm.BackupVersion,
		ClusterWideEnabled:      &vm.ClusterWideEnabled,
		CustomResourceUID:       &vm.CRUID,
		DatabaseVersion:         &vm.MongoVersion,
		HashicorpVaultEnabled:   &vm.HashicorpVaultEnabled,
		KubeVersion:             &vm.KubeVersion,
		OperatorVersion:         vm.Version,
		Platform:                &vm.Platform,
		PmmVersion:              &vm.PMMVersion,
		Product:                 productName,
		ShardingEnabled:         &vm.ShardingEnabled,
		PmmEnabled:              &vm.PMMEnabled,
		HelmDeployOperator:      &vm.HelmDeployOperator,
		HelmDeployCr:            &vm.HelmDeployCR,
		SidecarsUsed:            &vm.SidecarsUsed,
		BackupsEnabled:          &vm.BackupsEnabled,
		ClusterSize:             &vm.ClusterSize,
		PitrEnabled:             &vm.PITREnabled,
		PhysicalBackupScheduled: &vm.PhysicalBackupScheduled,
		McsEnabled:              &vm.MCSEnabled,
		RoleManagementEnabled:   &vm.RoleManagementEnabled,
		UserManagementEnabled:   &vm.UserManagementEnabled,
		VolumeExpansionEnabled:  &vm.VolumeExpansionEnabled,
	}
	applyParams = applyParams.WithTimeout(10 * time.Second)

	resp, err := vsClient.VersionService.VersionServiceApply(applyParams)

	if err != nil {
		return DepVersion{}, errors.Wrapf(err, "failed to version service apply")
	}

	if !versionUpgradeEnabled(cr) {
		return DepVersion{}, nil
	}

	if len(resp.Payload.Versions) == 0 {
		return DepVersion{}, fmt.Errorf("empty versions response")
	}

	mongoVersion, err := getVersion(resp.Payload.Versions[0].Matrix.Mongod)
	if err != nil {
		return DepVersion{}, errors.Wrapf(err, "get mongo version")
	}

	backupVersion, err := getVersion(resp.Payload.Versions[0].Matrix.Backup)
	if err != nil {
		return DepVersion{}, errors.Wrapf(err, "get backup version")
	}

	pmmVersion, err := getPMMVersion(resp.Payload.Versions[0].Matrix.Pmm, opts.PMM3Enabled)
	if err != nil {
		return DepVersion{}, errors.Wrapf(err, "get pmm version")
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

func getPMMVersion(versions map[string]models.VersionVersion, isPMM3 bool) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("response has zero versions")
	}
	// One version for PMM3 and one version for PMM2 should only exist.
	if len(versions) > 2 {
		return "", fmt.Errorf("response has more than 2 versions")
	}

	var pmm2Version, pmm3Version string
	for version := range versions {
		if strings.HasPrefix(version, "3.") {
			pmm3Version = version
		}
		if strings.HasPrefix(version, "2.") {
			pmm2Version = version
		}
	}

	if isPMM3 && pmm3Version == "" {
		return "", fmt.Errorf("pmm3 is configured, but no pmm3 version exists")
	}
	if isPMM3 && pmm3Version != "" {
		return pmm3Version, nil
	}
	if pmm2Version != "" {
		return pmm2Version, nil
	}

	return "", fmt.Errorf("no recognizable PMM version found")
}

type DepVersion struct {
	MongoImage    string `json:"mongoImage,omitempty"`
	MongoVersion  string `json:"mongoVersion,omitempty"`
	BackupImage   string `json:"backupImage,omitempty"`
	BackupVersion string `json:"backupVersion,omitempty"`
	PMMImage      string `json:"pmmImage,omitempty"`
	PMMVersion    string `json:"pmmVersion,omitempty"`
}

type versionOptions struct {
	PMM3Enabled bool
}

type VersionService interface {
	GetExactVersion(cr *api.PerconaServerMongoDB, endpoint string, vm VersionMeta, opts versionOptions) (DepVersion, error)
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
	Apply                   string
	MongoVersion            string
	KubeVersion             string
	Platform                string
	PMMVersion              string
	BackupVersion           string
	CRUID                   string
	Version                 string
	ClusterWideEnabled      bool
	HashicorpVaultEnabled   bool
	ShardingEnabled         bool
	PMMEnabled              bool
	HelmDeployOperator      bool
	HelmDeployCR            bool
	SidecarsUsed            bool
	BackupsEnabled          bool
	ClusterSize             int32
	PITREnabled             bool
	PhysicalBackupScheduled bool
	MCSEnabled              bool
	VolumeExpansionEnabled  bool
	UserManagementEnabled   bool
	RoleManagementEnabled   bool
}
