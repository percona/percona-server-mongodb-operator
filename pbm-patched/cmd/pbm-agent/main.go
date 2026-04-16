package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/fsnotify/fsnotify"
	mtLog "github.com/mongodb/mongo-tools/common/log"
	"github.com/mongodb/mongo-tools/common/options"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

const mongoConnFlag = "mongodb-uri"

func main() {
	rootCmd := rootCommand()
	rootCmd.AddCommand(versionCommand())
	rootCmd.AddCommand(util.CompletionCommand())

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}

func rootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "pbm-agent",
		Short: "Percona Backup for MongoDB",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := loadConfig(); err != nil {
				return err
			}
			return validateRootCommand()
		},
		Run: func(cmd *cobra.Command, args []string) {
			url := "mongodb://" + strings.Replace(viper.GetString(mongoConnFlag), "mongodb://", "", 1)

			hidecreds()

			logOpts := buildLogOpts()

			l := log.NewWithOpts(nil, "", "", logOpts).NewDefaultEvent()

			err := runAgent(url, viper.GetInt("backup.dump-parallel-collections"), logOpts)
			if err != nil {
				l.Error("Exit: %v", err)
				os.Exit(1)
			}
			l.Info("Exit: <nil>")
		},
	}

	rootCmd.CompletionOptions.DisableDefaultCmd = true

	setRootFlags(rootCmd)
	return rootCmd
}

func loadConfig() error {
	cfgFile := viper.GetString("config")
	if cfgFile == "" {
		return nil
	}

	viper.SetConfigFile(cfgFile)
	if err := viper.ReadInConfig(); err != nil {
		return errors.New("failed to read config: " + err.Error())
	}

	viper.WatchConfig()
	return nil
}

func validateRootCommand() error {
	if viper.GetString(mongoConnFlag) == "" {
		return errors.New("required flag " + mongoConnFlag + " not set")
	}

	if !isValidLogLevel(viper.GetString("log.level")) {
		return errors.New("invalid log level")
	}

	return nil
}

func setRootFlags(rootCmd *cobra.Command) {
	rootCmd.Flags().StringP("config", "f", "", "Path to the config file")
	_ = viper.BindPFlag("config", rootCmd.Flags().Lookup("config"))

	rootCmd.Flags().String(mongoConnFlag, "", "MongoDB connection string")
	_ = viper.BindPFlag(mongoConnFlag, rootCmd.Flags().Lookup(mongoConnFlag))
	_ = viper.BindEnv(mongoConnFlag, "PBM_MONGODB_URI")

	rootCmd.Flags().Int("dump-parallel-collections", 0, "Number of collections to dump in parallel")
	_ = viper.BindPFlag("backup.dump-parallel-collections", rootCmd.Flags().Lookup("dump-parallel-collections"))
	_ = viper.BindEnv("backup.dump-parallel-collections", "PBM_DUMP_PARALLEL_COLLECTIONS")
	viper.SetDefault("backup.dump-parallel-collections", max(runtime.NumCPU()/2, 1))

	rootCmd.Flags().String("log-path", "", "Path to file")
	_ = viper.BindPFlag("log.path", rootCmd.Flags().Lookup("log-path"))
	_ = viper.BindEnv("log.path", "LOG_PATH")
	viper.SetDefault("log.path", "/dev/stderr")

	rootCmd.Flags().Bool("log-json", false, "Enable JSON logging")
	_ = viper.BindPFlag("log.json", rootCmd.Flags().Lookup("log-json"))
	_ = viper.BindEnv("log.json", "LOG_JSON")
	viper.SetDefault("log.json", false)

	rootCmd.Flags().String("log-level", "",
		"Minimal log level based on severity level: D, I, W, E or F, low to high."+
			"Choosing one includes higher levels too.")
	_ = viper.BindPFlag("log.level", rootCmd.Flags().Lookup("log-level"))
	_ = viper.BindEnv("log.level", "LOG_LEVEL")
	viper.SetDefault("log.level", log.D)
}

func versionCommand() *cobra.Command {
	var (
		versionShort  bool
		versionCommit bool
		versionFormat string
	)

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "PBM version info",
		Run: func(cmd *cobra.Command, args []string) {
			switch {
			case versionShort:
				cmd.Println(version.Current().Short())
			case versionCommit:
				cmd.Println(version.Current().GitCommit)
			default:
				cmd.Println(version.Current().All(versionFormat))
			}
		},
	}

	versionCmd.Flags().BoolVar(&versionShort, "short", false, "Only version info")
	versionCmd.Flags().BoolVar(&versionCommit, "commit", false, "Only git commit info")
	versionCmd.Flags().StringVar(&versionFormat, "format", "", "Output format <json or \"\">")

	return versionCmd
}

func isValidLogLevel(logLevel string) bool {
	validLogLevels := []string{log.D, log.I, log.W, log.E, log.F}

	for _, validLevel := range validLogLevels {
		if logLevel == validLevel {
			return true
		}
	}

	return false
}

func buildLogOpts() *log.Opts {
	logLevel := viper.GetString("log.level")
	if !isValidLogLevel(logLevel) {
		fmt.Printf("Invalid log level: %s. Falling back to default.\n", logLevel)
		logLevel = log.D
	}

	return &log.Opts{
		LogPath:  viper.GetString("log.path"),
		LogLevel: logLevel,
		LogJSON:  viper.GetBool("log.json"),
	}
}

func runAgent(
	mongoURI string,
	dumpConns int,
	logOpts *log.Opts,
) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	leadConn, err := connect.Connect(ctx, mongoURI, "pbm-agent")
	if err != nil {
		return errors.Wrap(err, "connect to PBM")
	}

	err = setupNewDB(ctx, leadConn)
	if err != nil {
		return errors.Wrap(err, "setup pbm collections")
	}

	agent, err := newAgent(ctx, leadConn, mongoURI, dumpConns)
	if err != nil {
		return errors.Wrap(err, "connect to the node")
	}

	logger := log.NewWithOpts(
		agent.leadConn,
		agent.brief.SetName,
		agent.brief.Me,
		logOpts)
	defer logger.Close()

	viper.OnConfigChange(func(e fsnotify.Event) {
		logger.SetLogLevelAndJSON(buildLogOpts())
		newOpts := logger.Opts()
		logger.Printf("log options updated: log-path=%s, log-level:%s, log-json:%t",
			newOpts.LogPath, newOpts.LogLevel, newOpts.LogJSON)
	})

	ctx = log.SetLoggerToContext(ctx, logger)

	mtLog.SetDateFormat(log.LogTimeFormat)
	mtLog.SetVerbosity(&options.Verbosity{VLevel: mtLog.DebugLow})
	mtLog.SetWriter(logger)

	logger.Printf(perconaSquadNotice)
	logger.Printf("log options: log-path=%s, log-level:%s, log-json:%t",
		logOpts.LogPath, logOpts.LogLevel, logOpts.LogJSON)

	canRunSlicer := true
	if err := agent.CanStart(ctx); err != nil {
		if errors.Is(err, ErrArbiterNode) || errors.Is(err, ErrDelayedNode) {
			canRunSlicer = false
		} else {
			return errors.Wrap(err, "pre-start check")
		}
	}

	agent.showIncompatibilityWarning(ctx)

	if canRunSlicer {
		go agent.PITR(ctx)
	}
	go agent.HbStatus(ctx)

	return errors.Wrap(agent.Start(ctx), "listen the commands stream")
}
