package version

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/mod/semver"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

// current PBM version
const version = "2.8.0"

var (
	platform  string
	gitCommit string
	gitBranch string
	buildTime string
)

type Info struct {
	Version   string `json:"Version"`
	Platform  string `json:"Platform"`
	GitCommit string `json:"GitCommit"`
	GitBranch string `json:"GitBranch"`
	BuildTime string `json:"BuildTime"`
	GoVersion string `json:"GoVersion"`
}

const plain = `Version:   %s
Platform:  %s
GitCommit: %s
GitBranch: %s
BuildTime: %s
GoVersion: %s`

func Current() Info {
	v := Info{
		Version:   version,
		Platform:  platform,
		GitCommit: gitCommit,
		GitBranch: gitBranch,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
	}
	if v.Platform == "" {
		v.Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}

	return v
}

func (i Info) String() string {
	return fmt.Sprintf(plain,
		i.Version,
		i.Platform,
		i.GitCommit,
		i.GitBranch,
		i.BuildTime,
		i.GoVersion,
	)
}

func (i Info) Short() string {
	return i.Version
}

func (i Info) All(format string) string {
	switch format {
	case "":
		return fmt.Sprintf(plain,
			i.Version,
			i.Platform,
			i.GitCommit,
			i.GitBranch,
			i.BuildTime,
			i.GoVersion,
		)
	case "json":
		v, _ := json.MarshalIndent(i, "", " ") //nolint:errchkjson
		return string(v)
	default:
		return fmt.Sprintf("%#v", i)
	}
}

// CompatibleWith checks if a given version is compatible the current one. It
// is not compatible if the current is crossed the breaking ponit
// (version >= breakingVersion) and the given isn't (v < breakingVersion)
func CompatibleWith(v string, breakingv []string) bool {
	return compatible(version, v, breakingv)
}

func compatible(v1, v2 string, breakingv []string) bool {
	if len(breakingv) == 0 {
		return true
	}

	v1 = majmin(v1)
	v2 = majmin(v2)

	c := semver.Compare(v2, v1)
	if c == 0 {
		return true
	}

	hV, lV := v1, v2
	if c == 1 {
		hV, lV = lV, hV
	}

	for i := len(breakingv) - 1; i >= 0; i-- {
		cb := majmin(breakingv[i])
		if semver.Compare(hV, cb) >= 0 {
			return semver.Compare(lV, cb) >= 0
		}
	}

	return true
}

func majmin(v string) string {
	if len(v) == 0 {
		return v
	}

	if v[0] != 'v' {
		v = "v" + v
	}

	return semver.MajorMinor(v)
}

func canonify(ver string) string {
	if len(ver) == 0 {
		return ver
	}

	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}

	v := semver.Canonical(ver)
	v, _, _ = strings.Cut(v, "-") // cut prerelease
	v, _, _ = strings.Cut(v, "+") // cut build
	return v
}

func IsLegacyArchive(ver string) bool {
	return semver.Compare(majmin(ver), "v2.0") == -1
}

func IsLegacyBackupOplog(ver string) bool {
	return semver.Compare(majmin(ver), "v2.4") == -1
}

func HasFilelistFile(ver string) bool {
	// PBM-1252
	return semver.Compare(canonify(ver), "v2.4.1") != -1
}

// BreakingChangesMap map of versions introduced breaking changes to respective
// backup defs.
// !!! Versions should be sorted in the ascending order.
var BreakingChangesMap = map[defs.BackupType][]string{
	defs.LogicalBackup:     {"1.5.0"},
	defs.IncrementalBackup: {"2.1.0"},
	defs.PhysicalBackup:    {},
}

type MongoVersion struct {
	PSMDBVersion  string `bson:"psmdbVersion,omitempty"`
	VersionString string `bson:"version"`
	Version       []int  `bson:"versionArray"`
}

func (v MongoVersion) String() string {
	if v.PSMDBVersion != "" {
		return v.PSMDBVersion
	}

	return v.VersionString
}

func (v MongoVersion) Major() int {
	if len(v.Version) == 0 {
		return 0
	}

	return v.Version[0]
}

func (v MongoVersion) IsShardedTimeseriesSupported() bool {
	return v.Version[0] >= 6 // sharded timeseries introduced in 5.1
}

func (v MongoVersion) IsConfigShardSupported() bool {
	return v.Version[0] >= 8
}

func GetMongoVersion(ctx context.Context, m *mongo.Client) (MongoVersion, error) {
	res := m.Database("admin").RunCommand(ctx, bson.D{{"buildInfo", 1}})
	if err := res.Err(); err != nil {
		return MongoVersion{}, err
	}

	var ver MongoVersion
	if err := res.Decode(&ver); err != nil {
		return MongoVersion{}, err
	}

	return ver, nil
}

type FeatureSupport MongoVersion

func (f FeatureSupport) PBMSupport() error {
	v := MongoVersion(f)

	if (v.Version[0] >= 5 && v.Version[0] <= 8) && v.Version[1] == 0 {
		return nil
	}

	return errors.New("Unsupported MongoDB version. PBM works with v5.0, v6.0, v7.0, v8.0")
}

func (f FeatureSupport) FullPhysicalBackup() bool {
	// PSMDB 4.2.15, 4.4.6
	v := MongoVersion(f)
	if v.PSMDBVersion == "" {
		return false
	}

	switch {
	case v.Version[0] == 4 && v.Version[1] == 2 && v.Version[2] >= 15:
		fallthrough
	case v.Version[0] == 4 && v.Version[1] == 4 && v.Version[2] >= 6:
		fallthrough
	case v.Version[0] >= 5:
		return true
	}

	return false
}

func (f FeatureSupport) IncrementalPhysicalBackup() bool {
	// PSMDB 4.2.24, 4.4.18, 5.0.14, 6.0.3
	v := MongoVersion(f)
	if v.PSMDBVersion == "" {
		return false
	}

	switch {
	case v.Version[0] == 4 && v.Version[1] == 2 && v.Version[2] >= 24:
		fallthrough
	case v.Version[0] == 4 && v.Version[1] == 4 && v.Version[2] >= 18:
		fallthrough
	case v.Version[0] == 5 && v.Version[1] == 0 && v.Version[2] >= 14:
		fallthrough
	case v.Version[0] == 6 && v.Version[1] == 0 && v.Version[2] >= 3:
		fallthrough
	case v.Version[0] >= 7:
		return true
	}

	return false
}

func (f FeatureSupport) BackupType(t defs.BackupType) error {
	switch t {
	case defs.PhysicalBackup:
		if !f.FullPhysicalBackup() {
			return errors.New("full physical backup works since " +
				"Percona Server for MongoDB 4.2.15, 4.4.6")
		}
	case defs.IncrementalBackup:
		if !f.IncrementalPhysicalBackup() {
			return errors.New("incremental physical backup works since " +
				"Percona Server for MongoDB 4.2.24, 4.4.18, 5.0.14, 6.0.3")
		}
	case defs.ExternalBackup:
		if !f.FullPhysicalBackup() {
			return errors.New("external backup works since " +
				"Percona Server for MongoDB 4.2.15, 4.4.6")
		}
	}

	return nil
}

func GetFCV(ctx context.Context, m *mongo.Client) (string, error) {
	res := m.Database("admin").RunCommand(ctx, bson.D{
		{"getParameter", 1},
		{"featureCompatibilityVersion", 1},
	})
	if err := res.Err(); err != nil {
		return "", errors.Wrap(err, "query")
	}

	var ver struct{ FeatureCompatibilityVersion struct{ Version string } }
	if err := res.Decode(&ver); err != nil {
		return "", errors.Wrap(err, "decode")
	}

	return ver.FeatureCompatibilityVersion.Version, nil
}
