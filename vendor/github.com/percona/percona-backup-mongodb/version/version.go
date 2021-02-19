package version

import (
	"encoding/json"
	"fmt"
	"runtime"
)

// current PBM version
const version = "1.4.1"

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
	DefaultInfo.Version = version
	DefaultInfo.Platform = platform
	DefaultInfo.GitCommit = gitCommit
	DefaultInfo.GitBranch = gitBranch
	DefaultInfo.BuildTime = buildTime
	DefaultInfo.GoVersion = goVersion

	if DefaultInfo.Platform == "" {
		DefaultInfo.Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}
	DefaultInfo.GoVersion = runtime.Version()
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
