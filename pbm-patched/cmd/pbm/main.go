package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
	"github.com/percona/percona-backup-mongodb/sdk"
)

const (
	datetimeFormat = "2006-01-02T15:04:05"
	dateFormat     = "2006-01-02"
)

const (
	mongoConnFlag   = "mongodb-uri"
	RSMappingEnvVar = "PBM_REPLSET_REMAPPING"
	RSMappingFlag   = "replset-remapping"
	RSMappingDoc    = "re-map replset names for backups/oplog (e.g. to_name_1=from_name_1,to_name_2=from_name_2)"
)

type outFormat string

const (
	outJSON       outFormat = "json"
	outJSONpretty outFormat = "json-pretty"
	outText       outFormat = "text"
)

type logsOpts struct {
	tail     int64
	node     string
	severity string
	event    string
	opid     string
	location string
	extr     bool
	follow   bool
}

type cliResult interface {
	HasError() bool
}

type pbmApp struct {
	rootCmd *cobra.Command

	ctx     context.Context
	cancel  context.CancelFunc
	pbmOutF outFormat
	mURL    string
	conn    connect.Client
	pbm     *sdk.Client
	node    string
}

func main() {
	app := newPbmApp()
	if err := app.rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func (app *pbmApp) wrapRunE(
	fn func(cmd *cobra.Command, args []string) (fmt.Stringer, error),
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		out, err := fn(cmd, args)
		if err != nil {
			return err
		}
		printo(out, app.pbmOutF)
		return nil
	}
}

func (app *pbmApp) validateEnum(fieldName, value string, valid []string) error {
	if value == "" {
		return nil
	}

	for _, validItem := range valid {
		if value == validItem {
			return nil
		}
	}

	return errors.New(fmt.Sprintf("invalid %s value: %q (must be one of %v)", fieldName, value, valid))
}

func newPbmApp() *pbmApp {
	app := &pbmApp{}
	app.ctx, app.cancel = context.WithCancel(context.Background())

	app.rootCmd = &cobra.Command{
		Use:                "pbm",
		Short:              "Percona Backup for MongoDB",
		PersistentPreRunE:  app.persistentPreRun,
		PersistentPostRunE: app.persistentPostRun,
		SilenceUsage:       true,
	}

	app.rootCmd.CompletionOptions.DisableDefaultCmd = true

	app.rootCmd.PersistentFlags().String(
		mongoConnFlag,
		"",
		"MongoDB connection string (Default = PBM_MONGODB_URI environment variable)",
	)
	_ = viper.BindPFlag(mongoConnFlag, app.rootCmd.PersistentFlags().Lookup(mongoConnFlag))
	_ = viper.BindEnv(mongoConnFlag, "PBM_MONGODB_URI")

	app.rootCmd.PersistentFlags().StringP("out", "o", string(outText), "Output format <text>/<json>")
	_ = viper.BindPFlag("out", app.rootCmd.PersistentFlags().Lookup("out"))

	app.rootCmd.AddCommand(app.buildBackupCmd())
	app.rootCmd.AddCommand(app.buildBackupFinishCmd())
	app.rootCmd.AddCommand(app.buildCancelBackupCmd())
	app.rootCmd.AddCommand(app.buildConfigCmd())
	app.rootCmd.AddCommand(app.buildCleanupCmd())
	app.rootCmd.AddCommand(app.buildConfigProfileCmd())
	app.rootCmd.AddCommand(app.buildDeleteBackupCmd())
	app.rootCmd.AddCommand(app.buildDeletePitrCmd())
	app.rootCmd.AddCommand(app.buildDescBackupCmd())
	app.rootCmd.AddCommand(app.buildDescRestoreCmd())
	app.rootCmd.AddCommand(app.buildDiagnosticCmd())
	app.rootCmd.AddCommand(app.buildListCmd())
	app.rootCmd.AddCommand(app.buildLogCmd())
	app.rootCmd.AddCommand(app.buildRestoreCmd())
	app.rootCmd.AddCommand(app.buildReplayCmd())
	app.rootCmd.AddCommand(app.buildRestoreFinishCmd())
	app.rootCmd.AddCommand(app.buildStatusCmd())
	app.rootCmd.AddCommand(app.buildVersionCmd())
	app.rootCmd.AddCommand(util.CompletionCommand())

	return app
}

func (app *pbmApp) persistentPreRun(cmd *cobra.Command, args []string) error {
	app.pbmOutF = outFormat(viper.GetString("out"))

	if cmd.Name() == "help" || cmd.Name() == "version" || util.IsCompletionCommand(cmd.Name()) {
		return nil
	}

	app.mURL = viper.GetString(mongoConnFlag)
	if app.mURL == "" {
		return errors.New(
			"no MongoDB connection URI supplied\n" +
				"       Usual practice is the set it by the PBM_MONGODB_URI environment variable. " +
				"It can also be set with commandline argument --mongodb-uri.",
		)
	}

	if viper.GetString("describe-restore.config") != "" || viper.GetString("restore-finish.config") != "" {
		return nil
	}

	var err error
	app.conn, err = connect.Connect(app.ctx, app.mURL, "pbm-ctl")
	if err != nil {
		exitErr(errors.Wrap(err, "connect to mongodb"), app.pbmOutF)
	}
	app.ctx = log.SetLoggerToContext(app.ctx, log.New(app.conn, "", ""))

	ver, err := version.GetMongoVersion(app.ctx, app.conn.MongoClient())
	if err != nil {
		fmt.Fprintf(os.Stderr, "get mongo version: %v", err)
		os.Exit(1)
	}
	if err := version.FeatureSupport(ver).PBMSupport(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: %v\n", err)
	}

	app.pbm, err = sdk.NewClient(app.ctx, app.mURL)
	if err != nil {
		exitErr(errors.Wrap(err, "init sdk"), app.pbmOutF)
	}

	inf, err := topo.GetNodeInfo(app.ctx, app.conn.MongoClient())
	if err != nil {
		exitErr(errors.Wrap(err, "unable to obtain node info"), app.pbmOutF)
	}
	app.node = inf.Me

	return nil
}

func (app *pbmApp) persistentPostRun(cmd *cobra.Command, args []string) error {
	if app.pbm != nil {
		ctxPbm, cancelPbm := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelPbm()

		if err := app.pbm.Close(ctxPbm); err != nil {
			return errors.Wrap(err, "close pbm")
		}
	}

	if app.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := app.conn.Disconnect(ctx); err != nil {
			return errors.Wrap(err, "close conn")
		}
	}

	if app.cancel != nil {
		app.cancel()
	}

	return nil
}

func (app *pbmApp) buildBackupCmd() *cobra.Command {
	validCompressions := []string{
		string(compress.CompressionTypeNone),
		string(compress.CompressionTypeGZIP),
		string(compress.CompressionTypeSNAPPY),
		string(compress.CompressionTypeLZ4),
		string(compress.CompressionTypeS2),
		string(compress.CompressionTypePGZIP),
		string(compress.CompressionTypeZstandard),
	}

	validBackupTypes := []string{
		string(defs.PhysicalBackup),
		string(defs.LogicalBackup),
		string(defs.IncrementalBackup),
		string(defs.ExternalBackup),
	}

	backupOptions := backupOpts{}

	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Make backup",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if err := app.validateEnum("compression", backupOptions.compression, validCompressions); err != nil {
				return nil, err
			}

			if err := app.validateEnum("type", backupOptions.typ, validBackupTypes); err != nil {
				return nil, err
			}

			backupOptions.name = time.Now().UTC().Format(time.RFC3339)
			return runBackup(app.ctx, app.conn, app.pbm, &backupOptions, app.pbmOutF)
		}),
	}

	backupCmd.Flags().StringVar(
		&backupOptions.compression, "compression", "",
		"Compression type <none>/<gzip>/<snappy>/<lz4>/<s2>/<pgzip>/<zstd>",
	)
	backupCmd.Flags().StringVarP(
		&backupOptions.typ, "type", "t", string(defs.LogicalBackup),
		fmt.Sprintf(
			"backup type: <%s>/<%s>/<%s>/<%s>",
			defs.PhysicalBackup, defs.LogicalBackup, defs.IncrementalBackup, defs.ExternalBackup,
		),
	)
	backupCmd.Flags().BoolVar(
		&backupOptions.base, "base", false, "Is this a base for incremental backups",
	)
	backupCmd.Flags().StringVar(
		&backupOptions.profile, "profile", "", "Config profile name",
	)
	backupCmd.Flags().IntSliceVar(
		&backupOptions.compressionLevel, "compression-level", nil, "Compression level (specific to the compression type)",
	)
	backupCmd.Flags().Int32Var(
		&backupOptions.numParallelColls, "num-parallel-collections", 0, "Number of parallel collections",
	)
	backupCmd.Flags().StringVar(
		&backupOptions.ns, "ns", "", `Namespaces to backup (e.g. "db.*", "db.collection"). If not set, backup all ("*.*")`,
	)
	backupCmd.Flags().BoolVarP(
		&backupOptions.wait, "wait", "w", false, "Wait for the backup to finish",
	)
	backupCmd.Flags().DurationVar(
		&backupOptions.waitTime, "wait-time", 0, "Maximum wait time",
	)
	backupCmd.Flags().BoolVarP(
		&backupOptions.externList, "list-files", "l", false,
		"Shows the list of files per node to copy (only for external backups)",
	)

	return backupCmd
}

func (app *pbmApp) buildBackupFinishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup-finish [backup_name]",
		Short: "Finish external backup",
		Args:  cobra.MaximumNArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			finishBackupName := ""
			if len(args) == 1 {
				finishBackupName = args[0]
			}
			return runFinishBcp(app.ctx, app.conn, finishBackupName)
		}),
	}
}

func (app *pbmApp) buildCancelBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel-backup",
		Short: "Cancel backup",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return cancelBcp(app.ctx, app.pbm)
		}),
	}
}

func (app *pbmApp) buildCleanupCmd() *cobra.Command {
	cleanupOpts := cleanupOptions{}

	deletePitrCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete Backups and PITR chunks",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return doCleanup(app.ctx, app.conn, app.pbm, &cleanupOpts)
		}),
	}

	deletePitrCmd.Flags().StringVar(
		&cleanupOpts.olderThan, "older-than", "",
		fmt.Sprintf("Delete backups older than date/time in format %s or %s", datetimeFormat, dateFormat),
	)
	deletePitrCmd.Flags().BoolVarP(
		&cleanupOpts.yes, "yes", "y", false, "Don't ask for confirmation",
	)
	deletePitrCmd.Flags().BoolVarP(
		&cleanupOpts.wait, "wait", "w", false, "Wait for deletion done",
	)
	deletePitrCmd.Flags().DurationVar(
		&cleanupOpts.waitTime, "wait-time", 0, "Maximum wait time",
	)
	deletePitrCmd.Flags().BoolVar(
		&cleanupOpts.dryRun, "dry-run", false, "Report but do not delete",
	)

	return deletePitrCmd
}

func (app *pbmApp) buildConfigCmd() *cobra.Command {
	cfg := configOpts{set: make(map[string]string)}

	configCmd := &cobra.Command{
		Use:   "config [key]",
		Short: "Set, change or list the config",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if cfg.includeRestores && !cfg.rsync {
				return nil, errors.New("--include-restores cannot be used without --force-resync")
			}

			if len(args) == 1 {
				cfg.key = args[0]
			}
			return runConfig(app.ctx, app.conn, app.pbm, &cfg)
		}),
	}

	configCmd.Flags().BoolVar(&cfg.rsync, "force-resync", false, "Resync backup list with the current store")
	configCmd.Flags().BoolVar(
		&cfg.includeRestores, "include-restores", false, "Include physical restore metadata during force-resync",
	)
	configCmd.Flags().BoolVar(&cfg.list, "list", false, "List current settings")
	configCmd.Flags().StringVar(&cfg.file, "file", "", "Upload config from YAML file")
	configCmd.Flags().StringToStringVar(&cfg.set, "set", nil, "Set the option value <key.name=value>")
	configCmd.Flags().BoolVarP(&cfg.wait, "wait", "w", false, "Wait for finish")
	configCmd.Flags().DurationVar(&cfg.waitTime, "wait-time", 0, "Maximum wait time")

	return configCmd
}

func (app *pbmApp) buildConfigProfileCmd() *cobra.Command {
	runListConfigProfileCmd := app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
		if len(args) != 0 {
			return nil, errors.New(fmt.Sprintf("parse command line parameters: unexpected %s", args[0]))
		}

		return handleListConfigProfiles(app.ctx, app.pbm)
	})

	configProfileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Configuration profiles",
		RunE:  runListConfigProfileCmd,
	}

	listConfigProfileCmd := &cobra.Command{
		Use:   "list",
		Short: "List configuration profiles",
		RunE:  runListConfigProfileCmd,
	}

	configProfileCmd.AddCommand(listConfigProfileCmd)

	showConfigProfileOpts := showConfigProfileOptions{}
	showConfigProfileCmd := &cobra.Command{
		Use:   "show [profile-name]",
		Short: "Show configuration profile",
		Args:  cobra.ExactArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			showConfigProfileOpts.name = args[0]
			return handleShowConfigProfiles(app.ctx, app.pbm, showConfigProfileOpts)
		}),
	}

	configProfileCmd.AddCommand(showConfigProfileCmd)

	addConfigProfileOpts := addConfigProfileOptions{}
	addConfigProfileCmd := &cobra.Command{
		Use:   "add [profile-name] [file]",
		Short: "Save configuration profile",
		Args:  cobra.ExactArgs(2),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			addConfigProfileOpts.name = args[0]

			f, err := os.Open(args[1])
			if err != nil {
				return nil, errors.Wrap(err, "open config file")
			}
			defer f.Close()

			addConfigProfileOpts.file = f
			return handleAddConfigProfile(app.ctx, app.pbm, addConfigProfileOpts)
		}),
	}

	addConfigProfileCmd.Flags().BoolVar(
		&addConfigProfileOpts.sync, "sync", false, "Sync from the external storage",
	)
	addConfigProfileCmd.Flags().BoolVarP(
		&addConfigProfileOpts.wait, "wait", "w", false, "Wait for done by agents",
	)
	addConfigProfileCmd.Flags().DurationVar(
		&addConfigProfileOpts.waitTime, "wait-time", 0, "Maximum wait time",
	)

	configProfileCmd.AddCommand(addConfigProfileCmd)

	removeConfigProfileOpts := removeConfigProfileOptions{}
	removeConfigProfileCmd := &cobra.Command{
		Use:   "remove [profile-name]",
		Short: "Remove configuration profile",
		Args:  cobra.ExactArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			removeConfigProfileOpts.name = args[0]
			return handleRemoveConfigProfile(app.ctx, app.pbm, removeConfigProfileOpts)
		}),
	}

	removeConfigProfileCmd.Flags().BoolVarP(
		&removeConfigProfileOpts.wait, "wait", "w", false, "Wait for done by agents",
	)
	removeConfigProfileCmd.Flags().DurationVar(
		&removeConfigProfileOpts.waitTime, "wait-time", 0, "Maximum wait time",
	)

	configProfileCmd.AddCommand(removeConfigProfileCmd)

	syncConfigProfileOpts := syncConfigProfileOptions{}
	syncConfigProfileCmd := &cobra.Command{
		Use:   "sync [profile-name]",
		Short: "Sync backup list from configuration profile",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if len(args) == 1 {
				syncConfigProfileOpts.name = args[0]
			}
			return handleSyncConfigProfile(app.ctx, app.pbm, syncConfigProfileOpts)
		}),
	}

	syncConfigProfileCmd.Flags().BoolVar(
		&syncConfigProfileOpts.all, "all", false, "Sync from all external storages",
	)
	syncConfigProfileCmd.Flags().BoolVar(
		&syncConfigProfileOpts.clear, "clear", false, "Clear backup list (can be used with profile name or --all)",
	)
	syncConfigProfileCmd.Flags().BoolVarP(
		&syncConfigProfileOpts.wait, "wait", "w", false, "Wait for done by agents",
	)
	syncConfigProfileCmd.Flags().DurationVar(
		&syncConfigProfileOpts.waitTime, "wait-time", 0, "Maximum wait time",
	)

	configProfileCmd.AddCommand(syncConfigProfileCmd)

	return configProfileCmd
}

func (app *pbmApp) buildDeleteBackupCmd() *cobra.Command {
	validBackupTypes := []string{
		string(defs.PhysicalBackup),
		string(defs.LogicalBackup),
		string(defs.IncrementalBackup),
		string(defs.ExternalBackup),
		string(backup.SelectiveBackup),
	}

	deleteBcpOptions := deleteBcpOpts{}

	deleteBcpCmd := &cobra.Command{
		Use:   "delete-backup [name]",
		Short: "Delete a backup",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if deleteBcpOptions.bcpType != "" {
				if err := app.validateEnum("type", deleteBcpOptions.bcpType, validBackupTypes); err != nil {
					return nil, err
				}
			}

			if len(args) == 1 {
				deleteBcpOptions.name = args[0]
			}

			return deleteBackup(app.ctx, app.conn, app.pbm, &deleteBcpOptions)
		}),
	}

	deleteBcpCmd.Flags().StringVar(
		&deleteBcpOptions.olderThan, "older-than", "",
		fmt.Sprintf("Delete backups older than date/time in format %s or %s", datetimeFormat, dateFormat),
	)
	deleteBcpCmd.Flags().StringVarP(
		&deleteBcpOptions.bcpType, "type", "t", "",
		fmt.Sprintf(
			"backup type: <%s>/<%s>/<%s>/<%s>",
			defs.PhysicalBackup, defs.LogicalBackup, defs.IncrementalBackup, defs.ExternalBackup,
		),
	)
	deleteBcpCmd.Flags().BoolVarP(
		&deleteBcpOptions.yes, "yes", "y", false, "Don't ask for confirmation",
	)
	deleteBcpCmd.Flags().BoolVarP(
		&deleteBcpOptions.yes, "force", "f", false, "Don't ask for confirmation (deprecated)",
	)
	deleteBcpCmd.Flags().BoolVar(
		&deleteBcpOptions.dryRun, "dry-run", false, "Report but do not delete",
	)

	return deleteBcpCmd
}

func (app *pbmApp) buildDeletePitrCmd() *cobra.Command {
	deletePitrOptions := deletePitrOpts{}

	deletePitrCmd := &cobra.Command{
		Use:   "delete-pitr",
		Short: "Delete PITR chunks",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return deletePITR(app.ctx, app.conn, app.pbm, &deletePitrOptions)
		}),
	}

	deletePitrCmd.Flags().StringVar(
		&deletePitrOptions.olderThan, "older-than", "",
		fmt.Sprintf("Delete backups older than date/time in format %s or %s", datetimeFormat, dateFormat),
	)
	deletePitrCmd.Flags().BoolVarP(
		&deletePitrOptions.all, "all", "a", false,
		`Delete all chunks (deprecated). Use --older-than="0d"`,
	)
	deletePitrCmd.Flags().BoolVarP(
		&deletePitrOptions.yes, "yes", "y", false, "Don't ask for confirmation",
	)
	deletePitrCmd.Flags().BoolVarP(
		&deletePitrOptions.yes, "force", "f", false, "Don't ask for confirmation (deprecated)",
	)
	deletePitrCmd.Flags().BoolVarP(
		&deletePitrOptions.wait, "wait", "w", false, "Wait for deletion done",
	)
	deletePitrCmd.Flags().DurationVar(
		&deletePitrOptions.waitTime, "wait-time", 0, "Maximum wait time",
	)
	deletePitrCmd.Flags().BoolVar(
		&deletePitrOptions.dryRun, "dry-run", false, "Report but do not delete",
	)

	return deletePitrCmd
}

func (app *pbmApp) buildDescBackupCmd() *cobra.Command {
	descBackup := descBcp{}

	descBackupCmd := &cobra.Command{
		Use:   "describe-backup [backup_name]",
		Short: "Describe backup",
		Args:  cobra.MaximumNArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if len(args) == 1 {
				descBackup.name = args[0]
			}
			return describeBackup(app.ctx, app.pbm, &descBackup, app.node)
		}),
	}

	descBackupCmd.Flags().BoolVar(
		&descBackup.coll, "with-collections", false, "Show collections in backup",
	)

	return descBackupCmd
}

func (app *pbmApp) buildDescRestoreCmd() *cobra.Command {
	descRestoreOption := descrRestoreOpts{}

	descRestoreCmd := &cobra.Command{
		Use:   "describe-restore [name]",
		Short: "Describe restore",
		Args:  cobra.MaximumNArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if len(args) == 1 {
				descRestoreOption.restore = args[0]
			}
			return describeRestore(app.ctx, app.conn, descRestoreOption, app.node)
		}),
	}

	descRestoreCmd.Flags().StringVarP(
		&descRestoreOption.cfg, "config", "c", "", "Show collections in backup",
	)
	_ = viper.BindPFlag("describe-restore.config", descRestoreCmd.Flags().Lookup("config"))

	return descRestoreCmd
}

func (app *pbmApp) buildDiagnosticCmd() *cobra.Command {
	diagnosticOpts := diagnosticOptions{}

	diagnosticCmd := &cobra.Command{
		Use:   "diagnostic",
		Short: "Create diagnostic report",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return handleDiagnostic(app.ctx, app.pbm, diagnosticOpts)
		}),
	}

	diagnosticCmd.Flags().StringVar(
		&diagnosticOpts.path, "path", "", "Path where files will be saved",
	)
	_ = diagnosticCmd.MarkFlagRequired("path")

	diagnosticCmd.Flags().BoolVar(
		&diagnosticOpts.archive, "archive", false, "Create zip file",
	)
	diagnosticCmd.Flags().StringVar(
		&diagnosticOpts.opid, "opid", "", "OPID/Command ID",
	)
	diagnosticCmd.Flags().StringVar(
		&diagnosticOpts.name, "name", "", "Backup or Restore name",
	)

	return diagnosticCmd
}

func (app *pbmApp) buildListCmd() *cobra.Command {
	listOptions := listOpts{}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Backup list",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return runList(app.ctx, app.conn, app.pbm, &listOptions)
		}),
	}

	listCmd.Flags().BoolVar(&listOptions.restore, "restore", false, "Show last N restores")
	listCmd.Flags().BoolVar(&listOptions.unbacked, "unbacked", false, "Show unbacked oplog ranges")
	listCmd.Flags().BoolVarP(&listOptions.full, "full", "f", false, "Show extended restore info")
	listCmd.Flags().IntVar(&listOptions.size, "size", 0, "Show last N backups")

	listCmd.Flags().StringVar(&listOptions.rsMap, RSMappingFlag, "", RSMappingDoc)
	_ = viper.BindPFlag(RSMappingFlag, listCmd.Flags().Lookup(RSMappingFlag))
	_ = viper.BindEnv(RSMappingFlag, RSMappingEnvVar)

	return listCmd
}

func (app *pbmApp) buildLogCmd() *cobra.Command {
	severityTypes := []string{
		"D", "I", "W", "E", "F",
	}

	logOptions := logsOpts{}
	logCmd := &cobra.Command{
		Use:   "logs",
		Short: "PBM logs",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if err := app.validateEnum("type", logOptions.severity, severityTypes); err != nil {
				return nil, err
			}

			return runLogs(app.ctx, app.conn, &logOptions, app.pbmOutF)
		}),
	}

	logCmd.Flags().BoolVarP(&logOptions.follow, "follow", "f", false, "Follow output")
	logCmd.Flags().Int64VarP(&logOptions.tail, "tail", "t", 20,
		"Show last N entries, 20 entries are shown by default, 0 for all logs",
	)
	logCmd.Flags().StringVarP(
		&logOptions.node, "node", "n", "",
		"Target node in format replset[/host:posrt]",
	)
	logCmd.Flags().StringVarP(
		&logOptions.severity, "severity", "s", "I",
		"Severity level D, I, W, E or F, low to high. Choosing one includes higher levels too.",
	)
	logCmd.Flags().StringVarP(
		&logOptions.event, "event", "e", "",
		"Event in format backup[/2020-10-06T11:45:14Z]. Events: backup, restore, cancelBackup, resync, pitr, delete",
	)
	logCmd.Flags().StringVar(
		&logOptions.location, "timezone", "",
		"Timezone of log output. `Local`, `UTC` or a location name corresponding to "+
			"a file in the IANA Time Zone database, such as `America/New_York`",
	)
	logCmd.Flags().StringVarP(&logOptions.opid, "opid", "i", "", "Operation ID")
	logCmd.Flags().BoolVarP(&logOptions.extr, "extra", "x", false, "Show extra data in text format")

	return logCmd
}

func (app *pbmApp) buildRestoreCmd() *cobra.Command {
	restoreOptions := restoreOpts{}

	restoreCmd := &cobra.Command{
		Use:   "restore [backup_name]",
		Short: "Restore backup",
		Args: func(cmd *cobra.Command, args []string) error {
			timeFlag, _ := cmd.Flags().GetString("time")
			external, _ := cmd.Flags().GetBool("external")

			hasBackup := len(args) > 0
			hasTime := timeFlag != ""

			if hasBackup && hasTime {
				return errors.New("backup name and --time cannot be used together")
			}
			if !external && !hasBackup && !hasTime {
				return errors.New("specify a backup name, --time, or --external")
			}

			return nil
		},
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if len(args) == 1 {
				restoreOptions.bcp = args[0]
			}
			if cmd.Flags().Changed("fallback-enabled") {
				val, _ := cmd.Flags().GetBool("fallback-enabled")
				restoreOptions.fallback = &val
			}
			if cmd.Flags().Changed("allow-partly-done") {
				val, _ := cmd.Flags().GetBool("allow-partly-done")
				restoreOptions.allowPartlyDone = &val
			}
			return runRestore(app.ctx, app.conn, app.pbm, &restoreOptions, app.node, app.pbmOutF)
		}),
	}

	restoreCmd.Flags().StringVar(
		&restoreOptions.pitr, "time", "",
		fmt.Sprintf("Restore to the point-in-time. Set in format %s", datetimeFormat),
	)
	restoreCmd.Flags().StringVar(
		&restoreOptions.pitrBase, "base-snapshot", "",
		"Override setting: Name of older snapshot that PITR will be based on during restore.",
	)
	restoreCmd.Flags().Int32Var(
		&restoreOptions.numParallelColls, "num-parallel-collections", 0,
		"Number of parallel collections",
	)
	restoreCmd.Flags().Int32Var(
		&restoreOptions.numInsertionWorkers, "num-insertion-workers-per-collection", 0,
		"Specifies the number of insertion workers to run concurrently per collection. For large imports, "+
			"increasing the number of insertion workers may increase the speed of the import.",
	)
	restoreCmd.Flags().StringVar(
		&restoreOptions.ns, "ns", "",
		`Namespaces to restore (e.g. "db1.*,db2.collection2"). If not set, restore all ("*.*")`,
	)
	restoreCmd.Flags().StringVar(
		&restoreOptions.nsFrom, "ns-from", "",
		"Allows collection cloning (creating from the backup with different name) "+
			"and specifies source collection for cloning from.",
	)
	restoreCmd.Flags().StringVar(
		&restoreOptions.nsTo, "ns-to", "",
		"Allows collection cloning (creating from the backup with different name) "+
			"and specifies destination collection for cloning to.",
	)
	restoreCmd.Flags().BoolVar(
		&restoreOptions.usersAndRoles, "with-users-and-roles", false,
		"Includes users and roles for selected database (--ns flag)",
	)
	restoreCmd.Flags().BoolVarP(
		&restoreOptions.wait, "wait", "w", false, "Wait for the restore to finish",
	)
	restoreCmd.Flags().DurationVar(
		&restoreOptions.waitTime, "wait-time", 0, "Maximum wait time",
	)
	restoreCmd.Flags().BoolVarP(
		&restoreOptions.extern, "external", "x", false, "External restore.",
	)
	restoreCmd.Flags().StringVarP(
		&restoreOptions.conf, "config", "c", "",
		"Mongod config for the source data. External backups only!",
	)
	restoreCmd.Flags().StringVar(
		&restoreOptions.ts, "ts", "",
		"MongoDB cluster time to restore to. In <T,I> format (e.g. 1682093090,9). External backups only!",
	)
	restoreCmd.Flags().Bool(
		"fallback-enabled", false, "Enables/disables fallback strategy when performing a physical restore.",
	)
	restoreCmd.Flags().Bool(
		"allow-partly-done", false,
		"Allows partly done state of the cluster after physical restore. "+
			"If enabled (default), partly-done status for a RS will be treated as a successful restore. "+
			"If disabled, fallback will be applied when the cluster is partly-done.",
	)

	restoreCmd.Flags().StringVar(&restoreOptions.rsMap, RSMappingFlag, "", RSMappingDoc)
	_ = viper.BindPFlag(RSMappingFlag, restoreCmd.Flags().Lookup(RSMappingFlag))
	_ = viper.BindEnv(RSMappingFlag, RSMappingEnvVar)

	return restoreCmd
}

func (app *pbmApp) buildRestoreFinishCmd() *cobra.Command {
	finishRestore := descrRestoreOpts{}

	restoreFinishCmd := &cobra.Command{
		Use:   "restore-finish [restore_name]",
		Short: "Finish external restore",
		Args:  cobra.MaximumNArgs(1),
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			if len(args) == 1 {
				finishRestore.restore = args[0]
			}
			return runFinishRestore(finishRestore, app.node)
		}),
	}

	restoreFinishCmd.Flags().StringVarP(
		&finishRestore.cfg, "config", "c", "", "Path to PBM config",
	)
	_ = restoreFinishCmd.MarkFlagRequired("config")
	_ = viper.BindPFlag("restore-finish.config", restoreFinishCmd.Flags().Lookup("config"))

	return restoreFinishCmd
}

func (app *pbmApp) buildReplayCmd() *cobra.Command {
	replayOpts := replayOptions{}

	replayCmd := &cobra.Command{
		Use:   "oplog-replay",
		Short: "Replay oplog",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			return replayOplog(app.ctx, app.conn, app.pbm, replayOpts, app.node, app.pbmOutF)
		}),
	}

	replayCmd.Flags().StringVar(
		&replayOpts.start, "start", "",
		fmt.Sprintf("Replay oplog from the time. Set in format %s", datetimeFormat),
	)
	_ = replayCmd.MarkFlagRequired("start")

	replayCmd.Flags().StringVar(
		&replayOpts.end, "end", "",
		fmt.Sprintf("Replay oplog from the time. Set in format %s", datetimeFormat),
	)
	_ = replayCmd.MarkFlagRequired("end")

	replayCmd.Flags().BoolVar(&replayOpts.wait, "wait", false, "Wait for the restore to finish")
	replayCmd.Flags().DurationVar(&replayOpts.waitTime, "wait-time", 0, "Maximum wait time")

	replayCmd.Flags().StringVar(&replayOpts.rsMap, RSMappingFlag, "", RSMappingDoc)
	_ = viper.BindPFlag(RSMappingFlag, replayCmd.Flags().Lookup(RSMappingFlag))
	_ = viper.BindEnv(RSMappingFlag, RSMappingEnvVar)

	return replayCmd
}

func (app *pbmApp) buildStatusCmd() *cobra.Command {
	sectionTypes := []string{
		"cluster", "pitr", "running", "backups",
	}

	statusOpts := statusOptions{}

	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"s"},
		Short:   "Show PBM status",
		RunE: app.wrapRunE(func(cmd *cobra.Command, args []string) (fmt.Stringer, error) {
			for _, value := range statusOpts.sections {
				if err := app.validateEnum("sections", value, sectionTypes); err != nil {
					return nil, err
				}
			}

			return status(app.ctx, app.conn, app.pbm, app.mURL, statusOpts, app.pbmOutF == outJSONpretty)
		}),
	}

	statusCmd.Flags().StringVar(&statusOpts.rsMap, RSMappingFlag, "", RSMappingDoc)
	_ = viper.BindPFlag(RSMappingFlag, statusCmd.Flags().Lookup(RSMappingFlag))
	_ = viper.BindEnv(RSMappingFlag, RSMappingEnvVar)

	statusCmd.Flags().StringArrayVarP(
		&statusOpts.sections, "sections", "s", []string{},
		"Sections of status to display <cluster>/<pitr>/<running>/<backups>.",
	)

	statusCmd.Flags().BoolVarP(
		&statusOpts.priority, "priority", "p", false, "Show backup and PITR priorities",
	)

	return statusCmd
}

func (app *pbmApp) buildVersionCmd() *cobra.Command {
	var (
		versionShort  bool
		versionCommit bool
	)

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "PBM version info",
		Run: func(cmd *cobra.Command, args []string) {
			var out fmt.Stringer
			switch {
			case versionCommit:
				out = outCaption{"GitCommit", version.Current().GitCommit}
			case versionShort:
				out = outCaption{"Version", version.Current().Version}
			default:
				out = version.Current()
			}

			printo(out, app.pbmOutF)
		},
	}

	versionCmd.Flags().BoolVarP(
		&versionShort, "short", "s", false, "Show only version info",
	)
	versionCmd.Flags().BoolVarP(
		&versionCommit, "commit", "c", false, "Show only git commit info",
	)

	return versionCmd
}

func printo(out fmt.Stringer, f outFormat) {
	if out == nil {
		return
	}

	switch f {
	case outJSON:
		err := json.NewEncoder(os.Stdout).Encode(out)
		if err != nil {
			exitErr(errors.Wrap(err, "encode output"), f)
		}
	case outJSONpretty:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		err := enc.Encode(out)
		if err != nil {
			exitErr(errors.Wrap(err, "encode output"), f)
		}
	default:
		fmt.Println(strings.TrimSpace(out.String()))
	}
}

func exitErr(e error, f outFormat) {
	switch f {
	case outJSON, outJSONpretty:
		var m interface{}
		m = e
		if _, ok := e.(json.Marshaler); !ok { //nolint:errorlint
			m = map[string]string{"Error": e.Error()}
		}

		j := json.NewEncoder(os.Stdout)
		if f == outJSONpretty {
			j.SetIndent("", "    ")
		}

		if err := j.Encode(m); err != nil {
			fmt.Fprintf(os.Stderr, "Error: encoding error \"%v\": %v", m, err)
		}
	default:
		fmt.Fprintln(os.Stderr, "Error:", e)
	}

	os.Exit(1)
}

func runLogs(ctx context.Context, conn connect.Client, l *logsOpts, f outFormat) (fmt.Stringer, error) {
	r := &log.LogRequest{}

	if l.node != "" {
		n := strings.Split(l.node, "/")
		r.RS = n[0]
		if len(n) > 1 {
			r.Node = n[1]
		}
	}

	if l.event != "" {
		e := strings.Split(l.event, "/")
		r.Event = e[0]
		if len(e) > 1 {
			r.ObjName = e[1]
		}
	}

	if l.opid != "" {
		r.OPID = l.opid
	}

	switch l.severity {
	case "F":
		r.Severity = log.Fatal
	case "E":
		r.Severity = log.Error
	case "W":
		r.Severity = log.Warning
	case "I":
		r.Severity = log.Info
	case "D":
		r.Severity = log.Debug
	default:
		r.Severity = log.Info
	}

	if l.follow {
		err := followLogs(ctx, conn, r, r.Node == "", l.extr, f)
		return nil, err
	}

	o, err := log.LogGet(ctx, conn, r, l.tail)
	if err != nil {
		return nil, errors.Wrap(err, "get logs")
	}

	o.ShowNode = r.Node == ""
	o.Extr = l.extr

	// reverse list
	for i := len(o.Data)/2 - 1; i >= 0; i-- {
		opp := len(o.Data) - 1 - i
		o.Data[i], o.Data[opp] = o.Data[opp], o.Data[i]
	}

	err = o.SetLocation(l.location)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse timezone: %v\n\n", err)
	}

	return o, nil
}

func followLogs(ctx context.Context, conn connect.Client, r *log.LogRequest, showNode, expr bool, f outFormat) error {
	outC, errC := log.Follow(ctx, conn, r, false)

	var enc *json.Encoder
	switch f {
	case outJSON:
		enc = json.NewEncoder(os.Stdout)
	case outJSONpretty:
		enc = json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
	}

	for {
		select {
		case entry, ok := <-outC:
			if !ok {
				return nil
			}

			if f == outJSON || f == outJSONpretty {
				err := enc.Encode(entry)
				if err != nil {
					exitErr(errors.Wrap(err, "encode output"), f)
				}
			} else {
				fmt.Println(entry.Stringify(tsUTC, showNode, expr))
			}
		case err, ok := <-errC:
			if !ok {
				return nil
			}

			return err
		}
	}
}

func tsUTC(ts int64) string {
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

type snapshotStat struct {
	Name        string           `json:"name"`
	Namespaces  []string         `json:"nss,omitempty"`
	Size        int64            `json:"size,omitempty"`
	Status      defs.Status      `json:"status"`
	PrintStatus defs.PrintStatus `json:"printStatus"`
	Err         error            `json:"-"`
	ErrString   string           `json:"error,omitempty"`
	RestoreTS   int64            `json:"restoreTo"`
	PBMVersion  string           `json:"pbmVersion"`
	Type        defs.BackupType  `json:"type"`
	SrcBackup   string           `json:"src"`
	StoreName   string           `json:"storage,omitempty"`
}

type pitrRange struct {
	Err            error          `json:"error,omitempty"`
	Range          oplog.Timeline `json:"range"`
	NoBaseSnapshot bool           `json:"noBaseSnapshot,omitempty"`
}

func (pr pitrRange) String() string {
	return fmt.Sprintf("{ %s }", pr.Range)
}

// fmtTS converts timestamp to Zulu time string representation,
// and removes Z identificator: 2025-02-05T11:04:59
func fmtTS(ts int64) string {
	t := time.Unix(ts, 0).UTC().Format(time.RFC3339)
	return strings.TrimSuffix(t, "Z")
}

type outMsg struct {
	Msg string `json:"msg"`
}

func (m outMsg) String() string {
	return m.Msg
}

type outCaption struct {
	k string
	v interface{}
}

func (c outCaption) String() string {
	return fmt.Sprint(c.v)
}

func (c outCaption) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{")
	b.WriteString(fmt.Sprintf("\"%s\":", c.k))
	err := json.NewEncoder(&b).Encode(c.v)
	if err != nil {
		return nil, err
	}
	b.WriteString("}")
	return b.Bytes(), nil
}

func cancelBcp(ctx context.Context, pbm *sdk.Client) (fmt.Stringer, error) {
	if _, err := pbm.CancelBackup(ctx); err != nil {
		return nil, errors.Wrap(err, "send backup canceling")
	}
	return outMsg{"Backup cancellation has started"}, nil
}

var errInvalidFormat = errors.New("invalid format")

func parseDateT(v string) (time.Time, error) {
	switch len(v) {
	case len(datetimeFormat):
		return time.Parse(datetimeFormat, v)
	case len(dateFormat):
		return time.Parse(dateFormat, v)
	}

	return time.Time{}, errInvalidFormat
}
