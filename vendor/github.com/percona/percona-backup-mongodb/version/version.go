package version

import (
	"encoding/json"
	"fmt"
	"runtime"

	"golang.org/x/mod/semver"
)

// current PBM version
const version = "1.6.1"

// !!! should be sorted in the ascending order
var breakingChangesV = []string{
	"1.5.0",
}

var (
	platform  string
	gitCommit string
	gitBranch string
	buildTime string
	goVersion string
)

type Info struct {
	Version   string
	Platform  string
	GitCommit string
	GitBranch string
	BuildTime string
	GoVersion string
}

const plain = `Version:   %s
Platform:  %s
GitCommit: %s
GitBranch: %s
BuildTime: %s
GoVersion: %s`

var DefaultInfo Info

func init() {
	DefaultInfo = Current()
}

func Current() (v Info) {
	v.Version = version
	v.Platform = platform
	v.GitCommit = gitCommit
	v.GitBranch = gitBranch
	v.BuildTime = buildTime
	v.GoVersion = goVersion

	if v.Platform == "" {
		v.Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}
	v.GoVersion = runtime.Version()

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
		v, _ := json.MarshalIndent(i, "", " ")
		return string(v)
	default:
		return fmt.Sprintf("%#v", i)
	}
}

// CompatibleWith tells if a given versions are compatible. Versions are not compatible
// if one is crossed the breaking ponit (v1 >= breakingVersion) and the other isn't (v2 < breakingVersion)
func Compatible(v1, v2 string) bool {
	return compatible(v1, v2, breakingChangesV)
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
