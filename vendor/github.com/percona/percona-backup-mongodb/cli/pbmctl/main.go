package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"

	"text/template"

	"github.com/alecthomas/kingpin"
	"github.com/percona/percona-backup-mongodb/internal/templates"
	"github.com/percona/percona-backup-mongodb/internal/utils"
	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/testdata"
)

// some vars are set by goreleaser
var (
	version               = "dev"
	commit                = "none"
	usageWriter io.Writer = os.Stdout // for testing purposes. In tests, we redirect this to a bytes buffer
	conn        *grpc.ClientConn

	grpcCompressors = []string{
		gzip.Name,
		"none",
	}

	backuptypes = []string{
		"hot",
		"logical",
	}

	destinationTypes = []string{
		"file",
		"aws",
	}
)

const (
	defaultBackupType       = "logical"
	defaultConfigFile       = "~/.percona-backup-mongodb.yaml"
	defaultDestinationType  = "file"
	defaultServerAddress    = "127.0.0.1:10001"
	defaultServerCompressor = "gzip"
	defaultSkipUserAndRoles = true
	defaultTlsEnabled       = false
)

type cliOptions struct {
	TLS              bool   `yaml:"tls" kingpin:"tls"`
	TLSCAFile        string `yaml:"tls_ca_file" kingpin:"tls-ca-file"`
	ServerAddress    string `yaml:"server_addr" kingpin:"server_addr"`
	ServerCompressor string `yaml:"server_compressor"`
	configFile       string `yaml:"-"`

	backup               *kingpin.CmdClause
	backupType           string
	destinationType      string
	compressionAlgorithm string
	encryptionAlgorithm  string
	description          string
	storageName          string

	restore                  *kingpin.CmdClause
	restoreMetadataFile      string
	restoreSkipUsersAndRoles bool

	list             *kingpin.CmdClause
	listBackups      *kingpin.CmdClause
	listNodes        *kingpin.CmdClause
	listNodesVerbose bool
	listStorages     *kingpin.CmdClause
}

func main() {
	cmd, opts, err := processCliArgs(os.Args[1:])
	if err != nil {
		if opts != nil {
			kingpin.Usage()
		}
		log.Fatal(err)
	}

	var grpcOpts []grpc.DialOption
	if opts.ServerCompressor != "" && opts.ServerCompressor != "none" {
		grpcOpts = append(grpcOpts, grpc.WithDefaultCallOptions(
			grpc.UseCompressor(opts.ServerCompressor),
		))
	}

	if opts.TLS {
		if opts.TLSCAFile == "" {
			opts.TLSCAFile = testdata.Path("ca.pem")
		}
		creds, err := credentials.NewClientTLSFromFile(opts.TLSCAFile, "")
		if err != nil {
			log.Fatalf("Failed to create TLS credentials %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(creds))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
	}

	ctx, cancel := context.WithCancel(context.Background())

	conn, err = grpc.Dial(opts.ServerAddress, grpcOpts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	apiClient := pbapi.NewApiClient(conn)
	if err != nil {
		log.Fatalf("Cannot connect to the API: %s", err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		<-c
		cancel()
	}()

	switch cmd {
	case "list nodes":
		clients, err := connectedAgents(ctx, conn)
		if err != nil {
			log.Errorf("Cannot get the list of connected agents: %s", err)
			break
		}
		if opts.listNodesVerbose {
			printTemplate(templates.ConnectedNodesVerbose, clients)
		} else {
			printTemplate(templates.ConnectedNodes, clients)
		}
	case "list backups":
		md, err := getAvailableBackups(ctx, conn)
		if err != nil {
			log.Errorf("Cannot get the list of available backups: %s", err)
			break
		}
		if len(md) > 0 {
			printTemplate(templates.AvailableBackups, md)
			return
		}
		fmt.Println("No backups found")
	case "list storages":
		storages, err := listStorages(ctx)
		if err != nil {
			log.Fatalf("Cannot get storages list: %s", err)
		}
		printTemplate(templates.AvailableStorages, storages)
	case "run backup":
		err := startBackup(ctx, apiClient, opts)
		if err != nil {
			log.Fatalf("Cannot send the StartBackup command to the gRPC server: %s", err)
		}
		log.Println("Backup completed")
	case "run restore":
		fmt.Println("restoring")
		err := restoreBackup(ctx, apiClient, opts)
		if err != nil {
			log.Fatalf("Cannot send the RestoreBackup command to the gRPC server: %s", err)
		}
		log.Println("Restore completed")
	default:
		log.Fatalf("Unknown command %q", cmd)
	}

	cancel()
}

func connectedAgents(ctx context.Context, conn *grpc.ClientConn) ([]*pbapi.Client, error) {
	apiClient := pbapi.NewApiClient(conn)
	stream, err := apiClient.GetClients(ctx, &pbapi.Empty{})
	if err != nil {
		return nil, err
	}
	clients := []*pbapi.Client{}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err, "Cannot get the connected agents list")
		}
		clients = append(clients, msg)
	}
	//if err := stream.CloseSend(); err != nil {
	//	return nil, errors.Wrap(err, "cannot close stream for connectedAgents function")
	//}
	sort.Slice(clients, func(i, j int) bool { return clients[i].NodeName < clients[j].NodeName })
	return clients, nil
}

func getAvailableBackups(ctx context.Context, conn *grpc.ClientConn) (map[string]*pb.BackupMetadata, error) {
	apiClient := pbapi.NewApiClient(conn)
	stream, err := apiClient.BackupsMetadata(ctx, &pbapi.BackupsMetadataParams{})
	if err != nil {
		return nil, err
	}

	mds := make(map[string]*pb.BackupMetadata)
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err, "Cannot get the connected agents list")
		}
		mds[msg.Filename] = msg.Metadata
	}

	return mds, nil
}

// This function is used by autocompletion. Currently, when it is called, the gRPC connection is nil
// because command line parameters havent been processed yet.
// Maybe in the future, we could read the defaults from a config file. For now, just try to connect
// to a server running on the local host
func listAvailableBackups() (backups []string) {
	var err error
	if conn == nil {
		conn, err = grpc.Dial(defaultServerAddress, []grpc.DialOption{grpc.WithInsecure()}...)
		if err != nil {
			return
		}
		defer conn.Close()
	}

	mds, err := getAvailableBackups(context.TODO(), conn)
	if err != nil {
		return
	}

	for name, md := range mds {
		backup := fmt.Sprintf("%s -> %s", name, md.Description)
		backups = append(backups, backup)
	}
	return
}

func listStorages(ctx context.Context) ([]pbapi.StorageInfo, error) {
	apiClient := pbapi.NewApiClient(conn)
	stream, err := apiClient.ListStorages(ctx, &pbapi.ListStoragesParams{})
	if err != nil {
		return nil, errors.Wrap(err, "Cannot list storages")
	}

	storages := []pbapi.StorageInfo{}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err, "A problem was found while receiving storages list from the server")
		}
		storages = append(storages, *msg)
	}

	return storages, nil
}

func printTemplate(tpl string, data interface{}) {
	var b bytes.Buffer
	tmpl := template.Must(template.New("").Parse(tpl))
	if err := tmpl.Execute(&b, data); err != nil {
		log.Fatal(err)
	}
	print(b.String())
}

func startBackup(ctx context.Context, apiClient pbapi.ApiClient, opts *cliOptions) error {
	msg := &pbapi.RunBackupParams{
		CompressionType: pbapi.CompressionType_COMPRESSION_TYPE_NO_COMPRESSION,
		Cypher:          pbapi.Cypher_CYPHER_NO_CYPHER,
		Description:     opts.description,
		StorageName:     opts.storageName,
	}

	switch opts.backupType {
	case "logical":
		msg.BackupType = pbapi.BackupType_BACKUP_TYPE_LOGICAL
	case "hot":
		msg.BackupType = pbapi.BackupType_BACKUP_TYPE_HOTBACKUP
	default:
		return fmt.Errorf("backup type %q is invalid", opts.backupType)
	}

	switch opts.compressionAlgorithm {
	case "none", "":
	case "gzip":
		msg.CompressionType = pbapi.CompressionType_COMPRESSION_TYPE_GZIP
	default:
		return fmt.Errorf("compression algorithm %q is invalid", opts.compressionAlgorithm)
	}

	switch opts.encryptionAlgorithm {
	case "":
	default:
		return fmt.Errorf("encryption is not implemented yet")
	}

	_, err := apiClient.RunBackup(ctx, msg)
	if err != nil {
		return err
	}

	return nil
}

func restoreBackup(ctx context.Context, apiClient pbapi.ApiClient, opts *cliOptions) error {
	msg := &pbapi.RunRestoreParams{
		MetadataFile:      opts.restoreMetadataFile,
		SkipUsersAndRoles: opts.restoreSkipUsersAndRoles,
		StorageName:       opts.storageName,
	}

	_, err := apiClient.RunRestore(ctx, msg)
	if err != nil {
		return err
	}

	return nil
}

func processCliArgs(args []string) (string, *cliOptions, error) {
	app := kingpin.New("pbmctl", "Percona Backup for MongoDB CLI")
	app.Version(fmt.Sprintf("%s version %s, git commit %s", app.Name, version, commit))

	runCmd := app.Command("run", "Start a new backup or restore process")
	listCmd := app.Command("list", "List objects (connected nodes, backups, etc)")
	listBackupsCmd := listCmd.Command("backups", "List backups")
	listNodesCmd := listCmd.Command("nodes", "List objects (connected nodes, backups, etc)")
	listStoragesCmd := listCmd.Command("storages", "List available storageds")
	backupCmd := runCmd.Command("backup", "Start a backup")
	restoreCmd := runCmd.Command("restore", "Restore a backup given a metadata file name")

	opts := &cliOptions{
		list:         listCmd,
		listBackups:  listBackupsCmd,
		listNodes:    listNodesCmd,
		listStorages: listStoragesCmd,
		backup:       backupCmd,
		restore:      restoreCmd,
	}
	app.Flag("config-file", "Config file name").Short('c').StringVar(&opts.configFile)
	listNodesCmd.Flag("verbose", "Include extra node info").BoolVar(&opts.listNodesVerbose)
	backupCmd.Flag("backup-type", "Backup type (logical or hot)").Default(defaultBackupType).EnumVar(&opts.backupType, backuptypes...)
	backupCmd.Flag("destination-type", "Backup destination type (file or aws)").Default(defaultDestinationType).EnumVar(&opts.destinationType, destinationTypes...)
	backupCmd.Flag("compression-algorithm", "Compression algorithm used for the backup").StringVar(&opts.compressionAlgorithm)
	backupCmd.Flag("encryption-algorithm", "Encryption algorithm used for the backup").StringVar(&opts.encryptionAlgorithm)
	backupCmd.Flag("description", "Backup description").Required().StringVar(&opts.description)
	backupCmd.Flag("storage", "Storage Name").Required().StringVar(&opts.storageName)

	restoreCmd.Arg("metadata-file", "Metadata file having the backup info for restore").HintAction(listAvailableBackups).Required().StringVar(&opts.restoreMetadataFile)
	restoreCmd.Flag("skip-users-and-roles", "Do not restore users and roles").Default(fmt.Sprintf("%v", defaultSkipUserAndRoles)).BoolVar(&opts.restoreSkipUsersAndRoles)
	restoreCmd.Flag("storage", "Storage Name").Required().StringVar(&opts.storageName)

	app.Flag("server-address", "Backup coordinator address (host:port)").Default(defaultServerAddress).Short('s').StringVar(&opts.ServerAddress)
	app.Flag("server-compressor", "Backup coordinator gRPC compression (gzip or none)").Default(defaultServerCompressor).EnumVar(&opts.ServerCompressor, grpcCompressors...)
	app.Flag("tls", "Connection uses TLS if true, else plain TCP").Default(fmt.Sprintf("%v", defaultTlsEnabled)).BoolVar(&opts.TLS)
	app.Flag("tls-ca-file", "The file containing the CA root cert file").StringVar(&opts.TLSCAFile)
	app.Terminate(nil) // Don't call os.Exit() on errors
	app.UsageWriter(usageWriter)

	app.PreAction(func(c *kingpin.ParseContext) error {
		if opts.configFile == "" {
			fn := utils.Expand(defaultConfigFile)
			if _, err := os.Stat(fn); err != nil {
				return nil
			} else {
				opts.configFile = fn
			}
		}
		return utils.LoadOptionsFromFile(opts.configFile, c, opts)
	})

	cmd, err := app.DefaultEnvars().Parse(args)
	if err != nil {
		return "", nil, err
	}

	if cmd == "" {
		return "", opts, fmt.Errorf("Invalid command")
	}

	return cmd, opts, nil
}
