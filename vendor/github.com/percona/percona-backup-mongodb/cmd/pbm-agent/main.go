package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/agent"
	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/percona/percona-backup-mongodb/version"
)

func main() {
	var (
		pbmCmd      = kingpin.New("pbm-agent", "Percona Backup for MongoDB")
		pbmAgentCmd = pbmCmd.Command("run", "Run agent").Default().Hidden()

		mURI = pbmAgentCmd.Flag("mongodb-uri", "MongoDB connection string").Envar("PBM_MONGODB_URI").Required().String()

		versionCmd    = pbmCmd.Command("version", "PBM version info")
		versionShort  = versionCmd.Flag("short", "Only version info").Default("false").Bool()
		versionCommit = versionCmd.Flag("commit", "Only git commit info").Default("false").Bool()
		versionFormat = versionCmd.Flag("format", "Output format <json or \"\">").Default("").String()
	)

	cmd, err := pbmCmd.DefaultEnvars().Parse(os.Args[1:])
	if err != nil && cmd != versionCmd.FullCommand() {
		log.Println("Error: Parse command line parameters:", err)
		return
	}

	if cmd == versionCmd.FullCommand() {
		switch {
		case *versionCommit:
			fmt.Println(version.DefaultInfo.GitCommit)
		case *versionShort:
			fmt.Println(version.DefaultInfo.Short())
		default:
			fmt.Println(version.DefaultInfo.All(*versionFormat))
		}
		return
	}

	log.Println(runAgent(*mURI))
}

func runAgent(mongoURI string) error {
	mongoURI = "mongodb://" + strings.Replace(mongoURI, "mongodb://", "", 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node, err := mongo.NewClient(options.Client().ApplyURI(mongoURI).SetAppName("pbm-agent-exec").SetDirect(true))
	if err != nil {
		return errors.Wrap(err, "create node client")
	}
	err = node.Connect(ctx)
	if err != nil {
		return errors.Wrap(err, "node connect")
	}

	err = node.Ping(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "node ping")
	}

	pbmClient, err := pbm.New(ctx, mongoURI, "pbm-agent")
	if err != nil {
		return errors.Wrap(err, "connect to mongodb")
	}

	agnt := agent.New(pbmClient)
	// TODO: pass only options and connect while createing a node?
	agnt.AddNode(ctx, node, mongoURI)

	fmt.Println("pbm agent is listening for the commands")
	return errors.Wrap(agnt.Start(), "listen the commands stream")
}
