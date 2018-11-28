package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	sdk "github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	stub "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/stub"
	version "github.com/Percona-Lab/percona-server-mongodb-operator/version"
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	mongodbOT "github.com/percona/mongodb-orchestration-tools"

	"github.com/alecthomas/kingpin"
	"github.com/sirupsen/logrus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var (
	GitCommit    string
	GitBranch    string
	resyncPeriod time.Duration
	verbose      bool
	versionLine  = fmt.Sprintf(
		"percona/percona-server-mongodb-operator Version: %v, git commit: %s (branch: %s)",
		version.Version, GitCommit, GitBranch,
	)
)

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
	logrus.Info(versionLine)
	logrus.Infof("percona/mongodb-orchestration-tools Version: %v", mongodbOT.Version)
}

func main() {
	app := kingpin.New("percona-server-mongodb-operator", "A Kubernetes operator for Percona Server for MongoDB")
	app.Version(versionLine)
	app.Flag("resyncPeriod", "Set the rate of resync from the Kubernetes API").Default("5s").Envar("RESYNC_PERIOD").DurationVar(&resyncPeriod)
	app.Flag("verbose", "Enable verbose logging").Envar("LOG_VERBOSE").BoolVar(&verbose)

	// parse flags
	_, err := app.Parse(os.Args[1:])
	if err != nil {
		logrus.Fatalf("Cannot parse command line: %v", err)
	}

	// optionally enable verbose logging
	if verbose {
		logrus.SetLevel(logrus.DebugLevel)
	}

	printVersion()
	opSdk.ExposeMetricsPort()

	resource := "psmdb.percona.com/v1alpha1"
	kind := "PerconaServerMongoDB"
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		logrus.Fatalf("failed to get watch namespace: %v", err)
	}

	logrus.Infof("Watching %s, %s, %s, %s", resource, kind, namespace, resyncPeriod)
	opSdk.Watch(resource, kind, namespace, resyncPeriod)
	opSdk.Handle(stub.NewHandler(sdk.NewClient()))
	opSdk.Run(context.TODO())
}
