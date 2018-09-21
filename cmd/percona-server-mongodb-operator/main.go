package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/alecthomas/kingpin"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	stub "github.com/timvaillancourt/percona-server-mongodb-operator/pkg/stub"

	"github.com/sirupsen/logrus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var (
	Version     = "0.0.1"
	runUser     = "1001"
	runGroup    = "1001"
	mongodImage = "percona/percona-server-mongodb:latest"
)

func printVersion(appName string, config *stub.Config) {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
	logrus.Infof("%s version: %v", appName, Version)
	logrus.Infof("\tmongodb image tag: %s", config.Image)
	logrus.Infof("\tmongodb replset name: %s", config.ReplsetName)
	logrus.Infof("\tmongodb replset node count: %d", config.NodeCount)
}

func main() {
	config := &stub.Config{PodName: "percona-server-mongodb"}
	appName := filepath.Base(os.Args[0])
	app := kingpin.New(appName, "Kubernetes Operator for Percona Server for MongoDB")
	app.Flag("mongod-count", "Number of mongod containers to run in the pod").Default("3").UintVar(&config.NodeCount)
	app.Flag("mongod-replset", "Name of the Percona Server for MongoDB Replica Set").Default("rs").StringVar(&config.ReplsetName)
	app.Flag("mongod-image", "Name of the Percona Server for MongoDB Docker image").Default(mongodImage).StringVar(&config.Image)
	app.Flag("mongod-uid", "UID of the Percona Server for MongoDB daemon").Default(runUser).Int64Var(&config.RunUser)
	app.Flag("mongod-gid", "GID of the Percona Server for MongoDB daemon").Default(runGroup).Int64Var(&config.RunGroup)
	app.Version(appName + " version " + Version)
	app.Parse(os.Args[1:])

	printVersion(appName, config)
	sdk.ExposeMetricsPort()

	resource := "cache.example.com/v1alpha1"
	kind := "PerconaServerMongoDB"
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		logrus.Fatalf("failed to get watch namespace: %v", err)
	}
	resyncPeriod := time.Duration(5) * time.Second
	logrus.Infof("Watching %s, %s, %s, %d", resource, kind, namespace, resyncPeriod)
	sdk.Watch(resource, kind, namespace, resyncPeriod)
	sdk.Handle(stub.NewHandler(config))
	sdk.Run(context.TODO())
}
