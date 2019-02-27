package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/globalsign/mgo"
	"github.com/percona/percona-backup-mongodb/grpc/client"
	"github.com/percona/percona-backup-mongodb/internal/logger"
	"github.com/percona/percona-backup-mongodb/internal/loghook"
	"github.com/percona/percona-backup-mongodb/internal/utils"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding/gzip"
	yaml "gopkg.in/yaml.v2"
)

type cliOptions struct {
	app                  *kingpin.Application
	configFile           string
	generateSampleConfig bool

	DSN              string `yaml:"dsn,omitempty" kingpin:"dsn"`
	Debug            bool   `yaml:"debug,omitempty" kingpin:"debug"`
	LogFile          string `yaml:"log_file,omitempty" kingpin:"log-file"`
	PIDFile          string `yaml:"pid_file,omitempty" kingpin:"pid-file"`
	Quiet            bool   `yaml:"quiet,omitempty" kingpin:"quiet"`
	ServerAddress    string `yaml:"server_address" kingping:"server-address"`
	ServerCompressor string `yaml:"server_compressor" kingpin:"server-compressor"`
	StoragesConfig   string `yaml:"storages_config" kingpin:"storages-config"`
	TLS              bool   `yaml:"tls,omitempty" kingpin:"tls"`
	TLSCAFile        string `yaml:"tls_ca_file,omitempty" kingpin:"tls-ca-file"`
	TLSCertFile      string `yaml:"tls_cert_file,omitempty" kingpin:"tls-cert-file"`
	TLSKeyFile       string `yaml:"tls_key_file,omitempty" kingpin:"tls-key-file"`
	UseSysLog        bool   `yaml:"use_syslog,omitempty" kingpin:"use-syslog"`

	// MongoDB connection options
	MongodbConnOptions client.ConnectionOptions `yaml:"mongodb_conn_options,omitempty"`

	// MongoDB connection SSL options
	MongodbSslOptions client.SSLOptions `yaml:"mongodb_ssl_options,omitempty"`
}

const (
	sampleConfigFile     = "config.sample.yml"
	defaultServerAddress = "127.0.0.1:10000"
	defaultMongoDBHost   = "127.0.0.1"
	defaultMongoDBPort   = "27017"
)

var (
	version         = "dev"
	commit          = "none"
	log             = logrus.New()
	program         = filepath.Base(os.Args[0])
	grpcCompressors = []string{
		gzip.Name,
		"none",
	}
)

func main() {
	opts, err := processCliArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line arguments: %s", err)
	}

	if opts.UseSysLog {
		log = logger.NewSyslogLogger()
	} else {
		log = logger.NewDefaultLogger(opts.LogFile)
	}

	if opts.Debug {
		log.SetLevel(logrus.DebugLevel)
	}

	if opts.generateSampleConfig {
		if err := writeSampleConfig(sampleConfigFile, opts); err != nil {
			log.Fatalf("Cannot write sample config file %s: %s", sampleConfigFile, err)
		}
		log.Printf("Sample config was written to %s\n", sampleConfigFile)
		return
	}

	if opts.Quiet {
		log.SetLevel(logrus.ErrorLevel)
	}
	if opts.Debug {
		log.SetLevel(logrus.DebugLevel)
	}
	log.SetLevel(logrus.DebugLevel)

	log.Infof("Starting %s version %s, git commit %s", program, version, commit)

	grpcOpts := getgRPCOptions(opts)

	rand.Seed(time.Now().UnixNano())

	// Connect to the percona-backup-mongodb gRPC server
	conn, err := grpc.Dial(opts.ServerAddress, grpcOpts...)
	if err != nil {
		log.Fatalf("Fail to connect to the gRPC server at %q: %v", opts.ServerAddress, err)
	}
	defer conn.Close()
	log.Infof("Connected to the gRPC server at %s", opts.ServerAddress)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to the MongoDB instance
	var di *mgo.DialInfo
	if opts.DSN == "" {
		di = &mgo.DialInfo{
			Addrs:          []string{opts.MongodbConnOptions.Host + ":" + opts.MongodbConnOptions.Port},
			Username:       opts.MongodbConnOptions.User,
			Password:       opts.MongodbConnOptions.Password,
			ReplicaSetName: opts.MongodbConnOptions.ReplicasetName,
			FailFast:       true,
			Source:         "admin",
		}
	} else {
		di, err = mgo.ParseURL(opts.DSN)
		di.FailFast = true
		if err != nil {
			log.Fatalf("Cannot parse MongoDB DSN %q, %s", opts.DSN, err)
		}
	}
	mdbSession := &mgo.Session{}
	connectionAttempts := 0
	for {
		connectionAttempts++
		mdbSession, err = mgo.DialWithInfo(di)
		if err != nil {
			log.Errorf("Cannot connect to MongoDB at %s: %s", di.Addrs[0], err)
			if opts.MongodbConnOptions.ReconnectCount == 0 || connectionAttempts < opts.MongodbConnOptions.ReconnectCount {
				time.Sleep(time.Duration(opts.MongodbConnOptions.ReconnectDelay) * time.Second)
				continue
			}
			log.Fatalf("Could not connect to MongoDB. Retried every %d seconds, %d times", opts.MongodbConnOptions.ReconnectDelay, connectionAttempts)
		}
		break
	}

	log.Infof("Connected to MongoDB at %s", di.Addrs[0])
	defer mdbSession.Close()

	stg, err := storage.NewStorageBackendsFromYaml(utils.Expand(opts.StoragesConfig))
	if err != nil {
		log.Fatalf("Canot load storages config from file %s: %s", utils.Expand(opts.StoragesConfig), err)
	}

	input := client.InputOptions{
		DbConnOptions: opts.MongodbConnOptions,
		DbSSLOptions:  opts.MongodbSslOptions,
		GrpcConn:      conn,
		Logger:        log,
		Storages:      stg,
	}

	client, err := client.NewClient(context.Background(), input)
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Start(); err != nil {
		log.Fatalf("Cannot start client: %s", err)
	}

	logHook, err := loghook.NewGrpcLogging(ctx, client.ID(), conn)
	if err != nil {
		log.Fatalf("Failed to create gRPC log hook: %v", err)
	}
	logHook.SetLevel(log.Level)
	log.AddHook(logHook)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c
	client.Stop()
}

func processCliArgs(args []string) (*cliOptions, error) {
	app := kingpin.New("pbm-agent", "Percona Backup for MongoDB agent")
	app.Version(fmt.Sprintf("%s version %s, git commit %s", app.Name, version, commit))

	opts := &cliOptions{
		app:           app,
		ServerAddress: defaultServerAddress,
		MongodbConnOptions: client.ConnectionOptions{
			Host: defaultMongoDBHost,
			Port: defaultMongoDBPort,
		},
	}

	app.Flag("config-file", "Backup agent config file").Short('c').StringVar(&opts.configFile)
	app.Flag("debug", "Enable debug log level").Short('v').BoolVar(&opts.Debug)
	app.Flag("generate-sample-config", "Generate sample config.yml file with the defaults").BoolVar(&opts.generateSampleConfig)
	app.Flag("log-file", "Backup agent log file").Short('l').StringVar(&opts.LogFile)
	app.Flag("pid-file", "Backup agent pid file").StringVar(&opts.PIDFile)
	app.Flag("quiet", "Quiet mode. Log only errors").Short('q').BoolVar(&opts.Quiet)
	app.Flag("storages-config", "Storages config yaml file").StringVar(&opts.StoragesConfig)
	app.Flag("use-syslog", "Use syslog instead of Stderr or file").BoolVar(&opts.UseSysLog)
	//
	app.Flag("server-address", "Backup coordinator address (host:port)").Short('s').StringVar(&opts.ServerAddress)
	app.Flag("server-compressor", "Backup coordintor gRPC compression (gzip or none)").Default().EnumVar(&opts.ServerCompressor, grpcCompressors...)
	app.Flag("tls", "Use TLS for server connection").BoolVar(&opts.TLS)
	app.Flag("tls-cert-file", "TLS certificate file").ExistingFileVar(&opts.TLSCertFile)
	app.Flag("tls-key-file", "TLS key file").ExistingFileVar(&opts.TLSKeyFile)
	app.Flag("tls-ca-file", "TLS CA file").ExistingFileVar(&opts.TLSCAFile)
	//
	app.Flag("mongodb-dsn", "MongoDB connection string").StringVar(&opts.DSN)
	app.Flag("mongodb-host", "MongoDB hostname").Short('H').StringVar(&opts.MongodbConnOptions.Host)
	app.Flag("mongodb-port", "MongoDB port").Short('P').StringVar(&opts.MongodbConnOptions.Port)
	app.Flag("mongodb-username", "MongoDB username").Short('u').StringVar(&opts.MongodbConnOptions.User)
	app.Flag("mongodb-password", "MongoDB password").Short('p').StringVar(&opts.MongodbConnOptions.Password)
	app.Flag("mongodb-authdb", "MongoDB authentication database").StringVar(&opts.MongodbConnOptions.AuthDB)
	app.Flag("mongodb-replicaset", "MongoDB Replicaset name").StringVar(&opts.MongodbConnOptions.ReplicasetName)
	app.Flag("mongodb-reconnect-delay", "MongoDB reconnection delay in seconds").Default("10").IntVar(&opts.MongodbConnOptions.ReconnectDelay)
	app.Flag("mongodb-reconnect-count", "MongoDB max reconnection attempts (0: forever)").IntVar(&opts.MongodbConnOptions.ReconnectCount)

	app.PreAction(func(c *kingpin.ParseContext) error {
		if opts.configFile == "" {
			fn := utils.Expand("~/.percona-backup-mongodb.yaml")
			if _, err := os.Stat(fn); err != nil {
				return nil
			} else {
				opts.configFile = fn
			}
		}
		return utils.LoadOptionsFromFile(opts.configFile, c, opts)
	})

	_, err := app.DefaultEnvars().Parse(args)
	if err != nil {
		return nil, err
	}

	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	return opts, nil
}

func validateOptions(opts *cliOptions) error {
	opts.TLSCAFile = utils.Expand(opts.TLSCAFile)
	opts.TLSCertFile = utils.Expand(opts.TLSCertFile)
	opts.TLSKeyFile = utils.Expand(opts.TLSKeyFile)
	opts.PIDFile = utils.Expand(opts.PIDFile)

	if opts.PIDFile != "" {
		if err := writePidFile(opts.PIDFile); err != nil {
			return errors.Wrapf(err, "cannot write pid file %q", opts.PIDFile)
		}
	}

	if opts.DSN != "" {
		di, err := mgo.ParseURL(opts.DSN)
		if err != nil {
			return err
		}
		parts := strings.Split(di.Addrs[0], ":")
		if len(parts) < 2 {
			return fmt.Errorf("invalid host:port: %s", di.Addrs[0])
		}
		opts.MongodbConnOptions.Host = parts[0]
		opts.MongodbConnOptions.Port = parts[1]
		opts.MongodbConnOptions.User = di.Username
		opts.MongodbConnOptions.Password = di.Password
		opts.MongodbConnOptions.ReplicasetName = di.ReplicaSetName
	}

	return nil
}

func writeSampleConfig(filename string, opts *cliOptions) error {
	buf, err := yaml.Marshal(opts)
	if err != nil {
		return err
	}
	if err = ioutil.WriteFile(filename, buf, os.ModePerm); err != nil {
		return errors.Wrapf(err, "cannot write sample config to %s", filename)
	}
	return nil
}

func getgRPCOptions(opts *cliOptions) []grpc.DialOption {
	var grpcOpts []grpc.DialOption
	if opts.TLS {
		creds, err := credentials.NewClientTLSFromFile(opts.TLSCAFile, "")
		if err != nil {
			log.Fatalf("Failed to create TLS credentials %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(creds))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
	}
	if opts.ServerCompressor != "" && opts.ServerCompressor != "none" {
		grpcOpts = append(grpcOpts, grpc.WithDefaultCallOptions(
			grpc.UseCompressor(opts.ServerCompressor),
		))
	}
	return grpcOpts
}

// Write a pid file, but first make sure it doesn't exist with a running pid.
func writePidFile(pidFile string) error {
	// Read in the pid file as a slice of bytes.
	if piddata, err := ioutil.ReadFile(filepath.Clean(pidFile)); err == nil {
		// Convert the file contents to an integer.
		if pid, err := strconv.Atoi(string(piddata)); err == nil {
			// Look for the pid in the process list.
			if process, err := os.FindProcess(pid); err == nil {
				// Send the process a signal zero kill.
				if err := process.Signal(syscall.Signal(0)); err == nil {
					// We only get an error if the pid isn't running, or it's not ours.
					return fmt.Errorf("pid already running: %d", pid)
				}
			}
		}
	}
	// If we get here, then the pidfile didn't exist,
	// or the pid in it doesn't belong to the user running this app.
	return ioutil.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0664)
}
