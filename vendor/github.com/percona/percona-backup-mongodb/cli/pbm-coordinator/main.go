package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/alecthomas/kingpin"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/percona/percona-backup-mongodb/grpc/api"
	"github.com/percona/percona-backup-mongodb/grpc/server"
	"github.com/percona/percona-backup-mongodb/internal/logger"
	"github.com/percona/percona-backup-mongodb/internal/utils"
	apipb "github.com/percona/percona-backup-mongodb/proto/api"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/testdata"
)

type cliOptions struct {
	app        *kingpin.Application
	cmd        string
	configFile string
	//
	WorkDir              string `yaml:"work_dir" kingpin:"work-dir"`
	LogFile              string `yaml:"log_file" kingpin:"log-file"`
	Debug                bool   `yaml:"debug" kingpin:"debug"`
	UseSysLog            bool   `yaml:"sys_log_url" kingpin:"syslog-url"`
	APIBindIP            string `yaml:"api_bindip" kingpin:"api-bind-ip"`
	APIPort              int    `yaml:"api_port" kingpin:"api-port"`
	APIToken             string `yaml:"api_token" kingpin:"api-token"`
	GrpcBindIP           string `yaml:"grpc_bindip" kingpin:"grpc-bind-ip"`
	GrpcPort             int    `yaml:"grpc_port" kingpin:"grpc-port"`
	TLS                  bool   `yaml:"tls" kingpin:"tls"`
	TLSCertFile          string `yaml:"tls_cert_file" kingpin:"tls-cert-file"`
	TLSKeyFile           string `yaml:"tls_key_file" kingpin:"tls-key-file"`
	TLSCAFile            string `yaml:"tls_ca_file,omitempty" kingpin:"tls-ca-file"`
	EnableClientsLogging bool   `yaml:"enable_clients_logging" kingpin:"enable-clients-logging"`
	ClientsRefreshSecs   int    `yaml:"clients_refresh_secs" kingpin:"clients-refresh-secs"`
	ShutdownTimeout      int    `yaml:"shutdown_timeout" kingpin:"shutdown-timeout"`
	ServerCompressor     string `yaml:"server_compressor" kingpin:"server-compressor"`
}

type contextKey string

func (c contextKey) String() string {
	return string(c)
}

const (
	defaultGrpcPort        = 10000
	defaultAPIPort         = 10001
	defaultShutdownTimeout = 5 // Seconds
	defaultClientsLogging  = true
	defaultDebugMode       = false
	defaultWorkDir         = "~/percona-backup-mongodb"
)

var (
	log       = logrus.New()
	Version   = "dev"
	Commit    = "none"
	Build     = "date"
	Branch    = "master"
	GoVersion = "0.0.0"
)

func main() {
	program := filepath.Base(os.Args[0])

	opts, err := processCliParams(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line arguments: %s", err)
	}

	if opts.UseSysLog {
		log = logger.NewSyslogLogger()
	} else {
		log = logger.NewDefaultLogger(opts.LogFile)
	}

	if opts.Debug || os.Getenv("DEBUG") == "1" {
		log.SetLevel(logrus.DebugLevel)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.GrpcBindIP, opts.GrpcPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	apilis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.APIBindIP, opts.APIPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	var grpcOpts []grpc.ServerOption

	if opts.TLS {
		if opts.TLSCertFile == "" {
			opts.TLSCertFile = testdata.Path("server1.pem")
		}
		if opts.TLSKeyFile == "" {
			opts.TLSKeyFile = testdata.Path("server1.key")
		}
		creds, err := credentials.NewServerTLSFromFile(opts.TLSCertFile, opts.TLSKeyFile)
		if err != nil {
			log.Fatalf("Failed to generate credentials %v", err)
		}
		grpcOpts = []grpc.ServerOption{grpc.Creds(creds)}
	}

	log.Infof("Starting %s version %s, git commit %s", program, Version, Commit)

	stopChan := make(chan interface{})
	wg := &sync.WaitGroup{}

	var messagesServer *server.MessagesServer
	grpcServer := grpc.NewServer(grpcOpts...)

	if opts.EnableClientsLogging {
		messagesServer = server.NewMessagesServerWithClientLogging(opts.WorkDir, log)
	} else {
		messagesServer = server.NewMessagesServer(opts.WorkDir)
	}
	pb.RegisterMessagesServer(grpcServer, messagesServer)

	wg.Add(1)
	log.Printf("Starting agents gRPC server. Listening on %s", lis.Addr().String())
	runAgentsGRPCServer(grpcServer, lis, opts.ShutdownTimeout, stopChan, wg)

	apiGrpcOpts := make([]grpc.ServerOption, len(grpcOpts))
	copy(apiGrpcOpts, grpcOpts)
	apiGrpcOpts = append(apiGrpcOpts,
		grpc.StreamInterceptor(grpc_auth.StreamServerInterceptor(buildAuth(opts.APIToken))),
	)

	apiGrpcServer := grpc.NewServer(apiGrpcOpts...)
	apiServer := api.NewServer(messagesServer)
	apipb.RegisterApiServer(apiGrpcServer, apiServer)

	wg.Add(1)
	log.Infof("Starting API gRPC server. Listening on %s", apilis.Addr().String())
	runAgentsGRPCServer(apiGrpcServer, apilis, opts.ShutdownTimeout, stopChan, wg)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c
	log.Infof("Stop signal received. Stopping the server")
	close(stopChan)
	wg.Wait()
}

func runAgentsGRPCServer(grpcServer *grpc.Server, lis net.Listener, shutdownTimeout int,
	stopChan chan interface{}, wg *sync.WaitGroup) {
	go func() {
		err := grpcServer.Serve(lis)
		if err != nil {
			log.Printf("Cannot start agents gRPC server: %s", err)
		}
		wg.Done()
	}()

	go func() {
		<-stopChan
		log.Printf("Gracefully stopping server at %s", lis.Addr().String())
		// Try to Gracefully stop the gRPC server.
		c := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			c <- struct{}{}
		}()

		// If after shutdownTimeout seconds the server hasn't stop, just kill it.
		select {
		case <-c:
			return
		case <-time.After(time.Duration(shutdownTimeout) * time.Second):
			log.Printf("Stopping server at %s", lis.Addr().String())
			grpcServer.Stop()
		}
	}()
}

func processCliParams(args []string) (*cliOptions, error) {
	var err error
	app := kingpin.New("pbm-coordinator", "Percona Backup for MongoDB coordinator")
	app.Version(versionMessage())

	opts := &cliOptions{
		app:                  app,
		GrpcPort:             defaultGrpcPort,
		APIPort:              defaultAPIPort,
		EnableClientsLogging: defaultClientsLogging,
		ShutdownTimeout:      defaultShutdownTimeout,
		Debug:                defaultDebugMode,
		WorkDir:              defaultWorkDir,
	}
	grpcCompressors := []string{gzip.Name, "none"}

	app.Flag("config-file", "Config file").Default().Short('c').StringVar(&opts.configFile)
	app.Flag("work-dir", "Working directory for backup metadata").Short('d').StringVar(&opts.WorkDir)
	app.Flag("log-file", "Write logs to file").Short('l').StringVar(&opts.LogFile)
	app.Flag("debug", "Enable debug log level").Short('v').BoolVar(&opts.Debug)
	app.Flag("use-syslog", "Also send the logs to the local syslog server").BoolVar(&opts.UseSysLog)
	//
	app.Flag("grpc-bindip", "Bind IP for gRPC client connections").StringVar(&opts.GrpcBindIP)
	app.Flag("grpc-port", "Listening port for gRPC client connections").IntVar(&opts.GrpcPort)
	app.Flag("server-compressor", "Backup coordintor gRPC compression (gzip or none)").Default().
		EnumVar(&opts.ServerCompressor, grpcCompressors...)
	app.Flag("api-bindip", "Bind IP for API client connections").StringVar(&opts.APIBindIP)
	app.Flag("api-port", "Listening port for API client connections").IntVar(&opts.APIPort)
	app.Flag("api-token", "API token for clients connection").StringVar(&opts.APIToken)
	app.Flag("clients-refresh-secs", "Frequency in seconds to refresh state of clients").IntVar(&opts.ClientsRefreshSecs)
	app.Flag("enable-clients-logging", "Enable showing logs coming from agents on the server side").
		BoolVar(&opts.EnableClientsLogging)
	app.Flag("shutdown-timeout", "Server shutdown timeout").IntVar(&opts.ShutdownTimeout)
	//
	app.Flag("tls", "Enable TLS").BoolVar(&opts.TLS)
	app.Flag("tls-cert-file", "Cert file for gRPC client connections").StringVar(&opts.TLSCertFile)
	app.Flag("tls-key-file", "Key file for gRPC client connections").StringVar(&opts.TLSKeyFile)
	app.Flag("tls-ca-file", "TLS CA file").ExistingFileVar(&opts.TLSCAFile)

	app.PreAction(func(c *kingpin.ParseContext) error {
		if opts.configFile == "" {
			fn := utils.Expand("~/.percona-backup-mongodb.yaml")
			if _, err := os.Stat(fn); err != nil {
				return nil
			}
			opts.configFile = fn
		}
		return utils.LoadOptionsFromFile(opts.configFile, c, opts)
	})

	opts.cmd, err = app.DefaultEnvars().Parse(args)
	if err != nil {
		return nil, err
	}

	opts.WorkDir = utils.Expand(opts.WorkDir)
	opts.TLSCertFile = utils.Expand(opts.TLSCertFile)
	opts.TLSKeyFile = utils.Expand(opts.TLSKeyFile)

	if err = checkWorkDir(opts.WorkDir); err != nil {
		return nil, err
	}
	return opts, err
}

func checkWorkDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		log.Infof("Work dir %s doesn't exist. Creating it", dir)
		return os.MkdirAll(dir, os.ModePerm)
	}
	if !fi.IsDir() {
		return fmt.Errorf("cannot use %s for backups metadata. It is not a directory", dir)
	}
	return err
}

func buildAuth(wantToken string) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		token, err := grpc_auth.AuthFromMD(ctx, "bearer")
		if err != nil {
			return nil, err
		}
		if wantToken != token {
			return nil, status.Errorf(codes.Unauthenticated, "invalid auth token")
		}
		newCtx := context.WithValue(ctx, contextKey("tokenInfo"), time.Now().UTC().Format(time.RFC3339))
		return newCtx, nil
	}
}

func versionMessage() string {
	msg := "Version   : " + Version + "\n"
	msg += "Commit    : " + Commit + "\n"
	msg += "Build     : " + Build + "\n"
	msg += "Branch    : " + Branch + "\n"
	msg += "Go version: " + GoVersion + "\n"
	return msg
}
