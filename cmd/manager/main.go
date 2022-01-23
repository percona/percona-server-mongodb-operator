package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/operator-framework/operator-lib/leader"
	"os"
	"runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	_ "github.com/Percona-Lab/percona-version-service/api"
	certmgrscheme "github.com/jetstack/cert-manager/pkg/client/clientset/versioned/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	"github.com/percona/percona-server-mongodb-operator/pkg/controller"
)

var (
	GitCommit string
	GitBranch string
	log       = logf.Log.WithName("cmd")
)

func printVersion() {
	log.Info(fmt.Sprintf("Git commit: %s Git branch: %s", GitCommit, GitBranch))
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	//log.Info(fmt.Sprintf("operator-sdk Version: %v", sdkVersion.Version))
}

const watchNamespaceEnvVar = "WATCH_NAMESPACE"

func getWatchNamespace() (string, error) {
	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", watchNamespaceEnvVar)
	}
	return ns, nil
}

func main() {
	flag.Parse()

	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(zap.New(zap.UseDevMode(false)))

	printVersion()

	namespace, err := getWatchNamespace()
	if err != nil {
		log.Error(err, "failed to get watch namespace")
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Become the leader before proceeding
	leader.Become(context.TODO(), "percona-server-mongodb-operator-lock")

	// TODO: use new mechanics
	// https://sdk.operatorframework.io/docs/upgrading-sdk-version/v1.0.0/#removed-package-pkgready
	// https://github.com/operator-framework/operator-sdk/pull/3476
	//r := ready.NewFileReady()
	//err = r.Set()
	//if err != nil {
	//	log.Error(err, "")
	//	os.Exit(1)
	//}
	//defer r.Unset()

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{Namespace: namespace})
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Setup Scheme for cert-manager resources
	if err := certmgrscheme.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited non-zero")
		os.Exit(1)
	}
}
