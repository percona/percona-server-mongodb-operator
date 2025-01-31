package snapshot

import (
	"io"
	"runtime"
	"strings"

	"github.com/mongodb/mongo-tools/common/options"
	"github.com/mongodb/mongo-tools/mongorestore"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

const (
	preserveUUID = true

	batchSizeDefault = 500
)

var ExcludeFromRestore = []string{
	defs.DB + "." + defs.CmdStreamCollection,
	defs.DB + "." + defs.LogCollection,
	defs.DB + "." + defs.ConfigCollection,
	defs.DB + "." + defs.BcpCollection,
	defs.DB + "." + defs.RestoresCollection,
	defs.DB + "." + defs.LockCollection,
	defs.DB + "." + defs.LockOpCollection,
	defs.DB + "." + defs.PITRChunksCollection,
	defs.DB + "." + defs.PITRCollection,
	defs.DB + "." + defs.AgentsStatusCollection,
	defs.DB + "." + defs.PBMOpLogCollection,
	"admin.system.version",
	"config.version",
	"config.shards",

	// deprecated PBM collections, keep it here not to bring back from old backups
	defs.DB + ".pbmBackups.old",
	defs.DB + ".pbmPITRChunks.old",
}

type restorer struct{ *mongorestore.MongoRestore }

// CloneNS contains clone from/to info for cloning NS use case.
type CloneNS struct {
	FromNS string
	ToNS   string
}

// IsSpecified returns true in case of cloning use case.
func (c *CloneNS) IsSpecified() bool {
	return c.FromNS != "" && c.ToNS != ""
}

// SplitFromNS breaks cloning-from namespace to database & collection pair.
func (c *CloneNS) SplitFromNS() (string, string) {
	db, coll, _ := strings.Cut(c.FromNS, ".")
	return db, coll
}

// SplitToNS breaks cloning-to namespace to database & collection pair.
func (c *CloneNS) SplitToNS() (string, string) {
	db, coll, _ := strings.Cut(c.ToNS, ".")
	return db, coll
}

func NewRestore(uri string,
	cfg *config.Config,
	cloneNS CloneNS,
	numParallelColls,
	numInsertionWorkersPerCol int,
	excludeRouterCollections bool,
) (io.ReaderFrom, error) {
	topts := options.New("mongorestore",
		"0.0.1",
		"none",
		"",
		true,
		options.EnabledOptions{
			Auth:       true,
			Connection: true,
			Namespace:  true,
			URI:        true,
		})
	var err error
	topts.URI, err = options.NewURI(uri)
	if err != nil {
		return nil, errors.Wrap(err, "parse connection string")
	}

	err = topts.NormalizeOptionsAndURI()
	if err != nil {
		return nil, errors.Wrap(err, "parse opts")
	}

	topts.Direct = true
	topts.WriteConcern = writeconcern.Majority()

	batchSize := batchSizeDefault
	if cfg.Restore.BatchSize > 0 {
		batchSize = cfg.Restore.BatchSize
	}

	if numParallelColls < 1 {
		numParallelColls = 1
	}

	nsExclude := ExcludeFromRestore
	if excludeRouterCollections {
		configColls := []string{
			"config.databases",
			"config.collections",
			"config.chunks",
		}
		nsExclude := make([]string, len(ExcludeFromRestore)+len(configColls))
		n := copy(nsExclude, ExcludeFromRestore)
		copy(nsExclude[:n], configColls)
	}

	mopts := mongorestore.Options{}
	mopts.ToolOptions = topts
	mopts.InputOptions = &mongorestore.InputOptions{
		Archive: "-",
	}
	mopts.OutputOptions = &mongorestore.OutputOptions{
		BulkBufferSize:           batchSize,
		BypassDocumentValidation: true,
		Drop:                     true,
		NumInsertionWorkers:      numInsertionWorkersPerCol,
		NumParallelCollections:   numParallelColls,
		PreserveUUID:             preserveUUID,
		StopOnError:              true,
		WriteConcern:             "majority",
		NoIndexRestore:           true,
	}
	mopts.NSOptions = &mongorestore.NSOptions{
		NSExclude: nsExclude,
	}

	// in case of namespace cloning, we need to add/override following opts
	if cloneNS.IsSpecified() {
		mopts.NSOptions.NSInclude = []string{cloneNS.FromNS}
		mopts.NSFrom = []string{cloneNS.FromNS}
		mopts.NSTo = []string{cloneNS.ToNS}
		mopts.Drop = false
		mopts.PreserveUUID = false
	}

	// mongorestore calls runtime.GOMAXPROCS(MaxProcs).
	mopts.MaxProcs = runtime.GOMAXPROCS(0)

	mr, err := mongorestore.New(mopts)
	if err != nil {
		return nil, errors.Wrap(err, "create mongorestore obj")
	}
	mr.SkipUsersAndRoles = true

	return &restorer{mr}, nil
}

func (r *restorer) ReadFrom(from io.Reader) (int64, error) {
	defer r.Close()

	r.InputReader = from

	rdumpResult := r.Restore()
	if rdumpResult.Err != nil {
		return 0, errors.Wrapf(rdumpResult.Err, "restore mongo dump (successes: %d / fails: %d)",
			rdumpResult.Successes, rdumpResult.Failures)
	}

	return 0, nil
}
