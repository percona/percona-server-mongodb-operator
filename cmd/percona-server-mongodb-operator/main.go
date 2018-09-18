package main

import (
	"context"
	"runtime"
	"time"

	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	stub "github.com/timvaillancourt/percona-server-mongodb-operator/pkg/stub"

	"github.com/sirupsen/logrus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var (
	runUser                int64 = 1001
	runGroup               int64 = 1001
	mongodContainerDataDir       = "/data/db"
	mongodDataVolumeName         = "mongodb-data"
	mongodPortName               = "mongodb"
	mongodImage                  = "percona/percona-server-mongodb:3.6"
)

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
}

func main() {
	printVersion()

	sdk.ExposeMetricsPort()

	config := &stub.Config{
		RunUser:          runUser,
		RunGroup:         runGroup,
		ContainerDataDir: mongodContainerDataDir,
		DataVolumeName:   mongodDataVolumeName,
		PortName:         mongodPortName,
		Image:            mongodImage,
	}

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
