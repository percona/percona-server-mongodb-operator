package perconaservermongodb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (vs VersionServiceClient) GetExactVersion(vm versionMeta) (DepVersion, error) {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	requestURL, err := url.Parse(
		fmt.Sprintf("%s/v1/psmdb-operator/%s/%s",
			strings.TrimRight(vs.URL, "/"),
			vs.OpVersion,
			vm.Apply,
		),
	)
	if err != nil {
		return DepVersion{}, err
	}

	q := requestURL.Query()
	q.Add("databaseVersion", vm.MongoVersion)
	q.Add("kubeVersion", vm.KubeVersion)
	q.Add("platform", vm.Platform)
	q.Add("customResourceUID", vm.CRUID)

	if vm.PMMVersion != "" {
		q.Add("pmmVersion", vm.PMMVersion)
	}

	if vm.BackupVersion != "" {
		q.Add("backupVersion", vm.BackupVersion)
	}

	requestURL.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", requestURL.String(), nil)
	if err != nil {
		return DepVersion{}, err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return DepVersion{}, err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DepVersion{}, fmt.Errorf("received bad status code %s", resp.Status)
	}

	r := VersionResponse{}
	err = json.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return DepVersion{}, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if len(r.Versions) == 0 {
		return DepVersion{}, fmt.Errorf("empty versions response")
	}

	mongoVersion, err := getVersion(r.Versions[0].Matrix.Mongo)
	if err != nil {
		return DepVersion{}, err
	}

	backupVersion, err := getVersion(r.Versions[0].Matrix.Backup)
	if err != nil {
		return DepVersion{}, err
	}

	pmmVersion, err := getVersion(r.Versions[0].Matrix.PMM)
	if err != nil {
		return DepVersion{}, err
	}

	return DepVersion{
		MongoImage:    r.Versions[0].Matrix.Mongo[mongoVersion].ImagePath,
		MongoVersion:  mongoVersion,
		BackupImage:   r.Versions[0].Matrix.Backup[backupVersion].ImagePath,
		BackupVersion: backupVersion,
		PMMImage:      r.Versions[0].Matrix.PMM[pmmVersion].ImagePath,
		PMMVersion:    pmmVersion,
	}, nil
}

func getVersion(versions map[string]Version) (string, error) {
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
	GetExactVersion(vm versionMeta) (DepVersion, error)
}

type VersionServiceClient struct {
	URL       string
	OpVersion string
}

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

type versionMeta struct {
	Apply         string
	MongoVersion  string
	KubeVersion   string
	Platform      string
	PMMVersion    string
	BackupVersion string
	CRUID         string
}
