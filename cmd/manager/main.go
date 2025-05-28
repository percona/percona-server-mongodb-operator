package main

import (
	"flag"
	"os"
	"runtime"
	"strconv"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	certmgrscheme "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/scheme"
	"github.com/go-logr/logr"
	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsServer "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/controller"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
)

var (
	GitCommit string
	GitBranch string
	BuildTime string
	scheme    = k8sruntime.NewScheme()
	setupLog  = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apis.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	opts := zap.Options{
		Encoder: getLogEncoder(setupLog),
		Level:   getLogLevel(setupLog),
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Manager starting up", "gitCommit", GitCommit, "gitBranch", GitBranch,
		"buildTime", BuildTime, "goVersion", runtime.Version(), "os", runtime.GOOS, "arch", runtime.GOARCH)

	namespace, err := k8s.GetWatchNamespace()
	if err != nil {
		setupLog.Error(err, "failed to get watch namespace")
		os.Exit(1)
	}

	operatorNamespace, err := k8s.GetOperatorNamespace()
	if err != nil {
		setupLog.Error(err, "failed to get operators' namespace")
		os.Exit(1)
	}

	config, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get config")
		os.Exit(1)
	}

	options := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsServer.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "08db0feb.percona.com",
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 9443,
		}),
	}

	options.Controller.GroupKindConcurrency = map[string]int{
		"PerconaServerMongoDB." + psmdbv1.SchemeGroupVersion.Group:        1,
		"PerconaServerMongoDBBackup." + psmdbv1.SchemeGroupVersion.Group:  1,
		"PerconaServerMongoDBRestore." + psmdbv1.SchemeGroupVersion.Group: 1,
	}

	if s := os.Getenv("MAX_CONCURRENT_RECONCILES"); s != "" {
		if i, err := strconv.Atoi(s); err == nil && i > 0 {
			options.Controller.GroupKindConcurrency["PerconaServerMongoDB."+psmdbv1.SchemeGroupVersion.Group] = i
			options.Controller.GroupKindConcurrency["PerconaServerMongoDBBackup."+psmdbv1.SchemeGroupVersion.Group] = i
			options.Controller.GroupKindConcurrency["PerconaServerMongoDBRestore."+psmdbv1.SchemeGroupVersion.Group] = i
		} else {
			setupLog.Error(err, "MAX_CONCURRENT_RECONCILES must be a positive number")
			os.Exit(1)
		}
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE
	if len(namespace) > 0 {
		namespaces := make(map[string]cache.Config)
		for _, ns := range append(strings.Split(namespace, ","), operatorNamespace) {
			namespaces[ns] = cache.Config{}
		}
		options.Cache.DefaultNamespaces = namespaces
	}

	mgr, err := ctrl.NewManager(config, options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	// Setup Scheme for cert-manager resources
	if err := certmgrscheme.AddToScheme(mgr.GetScheme()); err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	if err := mcs.Register(discovery.NewDiscoveryClientForConfigOrDie(config)); err != nil {
		setupLog.Error(err, "failed to register multicluster service")
		os.Exit(1)
	}

	if mcs.IsAvailable() {
		setupLog.Info("Multi cluster services available",
			"group", mcs.MCSSchemeGroupVersion.Group,
			"version", mcs.MCSSchemeGroupVersion.Version)
		if err := mcs.AddToScheme(mgr.GetScheme()); err != nil {
			setupLog.Error(err, "")
			os.Exit(1)
		}
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

}

func getLogEncoder(log logr.Logger) zapcore.Encoder {
	consoleEnc := zapcore.NewConsoleEncoder(uzap.NewDevelopmentEncoderConfig())

	s, found := os.LookupEnv("LOG_STRUCTURED")
	if !found {
		return consoleEnc
	}

	useJson, err := strconv.ParseBool(s)
	if err != nil {
		log.Info("Cant't parse LOG_STRUCTURED env var, using console logger", "envVar", s)
		return consoleEnc
	}
	if !useJson {
		return consoleEnc
	}

	return zapcore.NewJSONEncoder(uzap.NewProductionEncoderConfig())
}

func getLogLevel(log logr.Logger) zapcore.LevelEnabler {
	l, found := os.LookupEnv("LOG_LEVEL")
	if !found {
		return zapcore.InfoLevel
	}

	switch strings.ToUpper(l) {
	case "DEBUG":
		return zapcore.DebugLevel
	case "INFO":
		return zapcore.InfoLevel
	case "ERROR":
		return zapcore.ErrorLevel
	default:
		log.Info("Unsupported log level", "level", l)
		return zapcore.InfoLevel
	}
}
