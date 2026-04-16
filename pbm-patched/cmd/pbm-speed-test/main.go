package main

import (
	"context"
	"fmt"
	stdlog "log"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage/blackhole"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type rootOpts struct {
	mURL             string
	sampleColF       string
	sampleSizeF      float64
	compression      string
	compressLevelArg []int
}

var validCompressions = []string{
	string(compress.CompressionTypeNone),
	string(compress.CompressionTypeGZIP),
	string(compress.CompressionTypeSNAPPY),
	string(compress.CompressionTypeLZ4),
	string(compress.CompressionTypeS2),
	string(compress.CompressionTypePGZIP),
	string(compress.CompressionTypeZstandard),
}

func main() {
	rootOptions := rootOpts{}
	rootCmd := &cobra.Command{
		Use:          "pbm-speed-test",
		Short:        "Percona Backup for MongoDB compression and upload speed test",
		SilenceUsage: true,
	}

	rootCmd.PersistentFlags().StringVar(&rootOptions.mURL, "mongodb-uri", "", "MongoDB connection string")
	_ = viper.BindPFlag("mongodb-uri", rootCmd.PersistentFlags().Lookup("mongodb-uri"))
	_ = viper.BindEnv("mongodb-uri", "PBM_MONGODB_URI")

	rootCmd.PersistentFlags().StringVarP(
		&rootOptions.sampleColF, "sample-collection", "c", "",
		"Set collection as the data source",
	)
	rootCmd.PersistentFlags().Float64VarP(
		&rootOptions.sampleSizeF, "size-gb", "s", 1,
		"Set data size in GB. Default 1",
	)
	rootCmd.PersistentFlags().StringVar(
		&rootOptions.compression, "compression", "",
		"Compression type <none>/<gzip>/<snappy>/<lz4>/<s2>/<pgzip>/<zstd>",
	)
	rootCmd.PersistentFlags().IntSliceVar(
		&rootOptions.compressLevelArg, "compression-level", nil, "Compression level (specific to the compression type)",
	)

	compressionCmd := &cobra.Command{
		Use:   "compression",
		Short: "Run compression test",
		RunE: func(cmd *cobra.Command, args []string) error {
			if rootOptions.mURL == "" {
				rootOptions.mURL = viper.GetString("mongodb-uri")
			}

			if err := validateEnum("compression", rootOptions.compression, validCompressions); err != nil {
				return err
			}

			var compressLevel *int

			if len(rootOptions.compressLevelArg) > 0 {
				compressLevel = &rootOptions.compressLevelArg[0]
			}

			cmd.Print("Test started ")
			testCompression(
				rootOptions.mURL,
				compress.CompressionType(rootOptions.compression),
				compressLevel,
				rootOptions.sampleSizeF,
				rootOptions.sampleColF,
			)

			return nil
		},
	}

	rootCmd.AddCommand(compressionCmd)

	storageCmd := &cobra.Command{
		Use:   "storage",
		Short: "Run storage test",
		RunE: func(cmd *cobra.Command, args []string) error {
			if rootOptions.mURL == "" {
				rootOptions.mURL = viper.GetString("mongodb-uri")
			}

			if err := validateEnum("compression", rootOptions.compression, validCompressions); err != nil {
				return err
			}

			var compressLevel *int

			if len(rootOptions.compressLevelArg) > 0 {
				compressLevel = &rootOptions.compressLevelArg[0]
			}

			cmd.Print("Test started ")
			testStorage(
				rootOptions.mURL,
				compress.CompressionType(rootOptions.compression),
				compressLevel,
				rootOptions.sampleSizeF,
				rootOptions.sampleColF,
			)

			return nil
		},
	}

	rootCmd.AddCommand(storageCmd)

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

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(util.CompletionCommand())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func testCompression(mURL string, compression compress.CompressionType, level *int, sizeGb float64, collection string) {
	ctx := context.Background()

	var cn *mongo.Client

	if collection != "" {
		cn, err := connect.MongoConnect(ctx, mURL, connect.Direct(true))
		if err != nil {
			stdlog.Fatalln("Error: connect to mongodb-node:", err)
		}
		defer cn.Disconnect(ctx) //nolint:errcheck
	}

	stg := blackhole.New()
	done := make(chan struct{})
	go printw(done)

	r, err := doTest(cn, stg, compression, level, sizeGb, collection)
	if err != nil {
		stdlog.Fatalln("Error:", err)
	}

	done <- struct{}{}
	fmt.Println()
	fmt.Println(r)
}

func testStorage(mURL string, compression compress.CompressionType, level *int, sizeGb float64, collection string) {
	sess, err := connect.MongoConnect(context.Background(), mURL, connect.Direct(true))
	if err != nil {
		stdlog.Fatalln("Error: connect to mongodb-node:", err)
	}
	defer sess.Disconnect(context.Background()) //nolint:errcheck

	client, err := connect.Connect(context.Background(), mURL, "pbm-speed-test")
	if err != nil {
		stdlog.Fatalln("Error: connect to mongodb-pbm:", err)
	}
	defer client.Disconnect(context.Background()) //nolint:errcheck

	stg, err := util.GetStorage(context.Background(), client, "", log.DiscardEvent)
	if err != nil {
		stdlog.Fatalln("Error: get storage:", err)
	}
	done := make(chan struct{})
	go printw(done)
	r, err := doTest(sess, stg, compression, level, sizeGb, collection)
	if err != nil {
		stdlog.Fatalln("Error:", err)
	}

	done <- struct{}{}
	fmt.Println()
	fmt.Println(r)
}

func printw(done <-chan struct{}) {
	tk := time.NewTicker(time.Second * 2)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			fmt.Print(".")
		case <-done:
			return
		}
	}
}

func validateEnum(fieldName, value string, valid []string) error {
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
