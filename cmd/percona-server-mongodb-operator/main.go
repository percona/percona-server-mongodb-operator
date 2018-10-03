package main

import (
	"context"
	"runtime"
	"time"

	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	stub "github.com/timvaillancourt/percona-server-mongodb-operator/pkg/stub"
	version "github.com/timvaillancourt/percona-server-mongodb-operator/version"

	mongodbOT "github.com/percona/mongodb-orchestration-tools"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	watchdog "github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"

	"github.com/sirupsen/logrus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
	logrus.Infof("perconalab/percona-server-mongodb-operator Version: %v", version.Version)
	logrus.Infof("\tpercona/mongodb-orchestration-tools Version: %v", mongodbOT.Version)
}

func main() {
	printVersion()
	sdk.ExposeMetricsPort()

	resource := "cache.example.com/v1alpha1"
	kind := "PerconaServerMongoDB"
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		logrus.Fatalf("failed to get watch namespace: %v", err)
	}

	operatorName, err := k8sutil.GetOperatorName()
	if err != nil {
		logrus.Fatalf("failed to get operator name: %v", err)
	}

	source := &podk8s.Pods{}
	quit := make(chan bool, 1)
	watchdog := watchdog.New(&wdConfig.Config{
		ServiceName:    operatorName,
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 10 * time.Second,
	}, &quit, source)
	go watchdog.Run()

	resyncPeriod := time.Duration(5) * time.Second
	logrus.Infof("Watching %s, %s, %s, %d", resource, kind, namespace, resyncPeriod)
	sdk.Watch(resource, kind, namespace, resyncPeriod)
	sdk.Handle(stub.NewHandler("mongodb"))
	sdk.Run(context.TODO())

	quit <- true
}
