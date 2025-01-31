package backup

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

const SelectiveBackup defs.BackupType = "selective"

var ErrInvalidDeleteBackupType = errors.New("invalid backup type")

func ParseDeleteBackupType(s string) (defs.BackupType, error) {
	if s == "" {
		return "", nil
	}

	switch s {
	case
		string(defs.PhysicalBackup),
		string(defs.ExternalBackup),
		string(defs.IncrementalBackup),
		string(defs.LogicalBackup),
		string(SelectiveBackup):
		return defs.BackupType(s), nil
	}

	return "", ErrInvalidDeleteBackupType
}

var (
	ErrBackupInProgress     = errors.New("backup is in progress")
	ErrIncrementalBackup    = errors.New("backup is incremental")
	ErrNonIncrementalBackup = errors.New("backup is not incremental")
	ErrNotBaseIncrement     = errors.New("backup is not base increment")
	ErrBaseForPITR          = errors.New("cannot delete the last PITR base snapshot while PITR is enabled")
)

type CleanupInfo struct {
	Backups []BackupMeta       `json:"backups"`
	Chunks  []oplog.OplogChunk `json:"chunks"`
}

// DeleteBackup deletes backup with the given name from the current storage
// and pbm database
func DeleteBackup(ctx context.Context, conn connect.Client, name, node string) error {
	bcp, err := NewDBManager(conn).GetBackupByName(ctx, name)
	if err != nil {
		return errors.Wrap(err, "get backup meta")
	}

	if bcp.Type == defs.IncrementalBackup {
		return deleteIncremetalChainImpl(ctx, conn, bcp, node)
	}

	return deleteBackupImpl(ctx, conn, bcp, node)
}

func deleteBackupImpl(
	ctx context.Context,
	conn connect.Client,
	bcp *BackupMeta,
	node string,
) error {
	err := CanDeleteBackup(ctx, conn, bcp)
	if err != nil {
		return err
	}

	stg, err := util.StorageFromConfig(&bcp.Store.StorageConf, node, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	err = DeleteBackupFiles(stg, bcp.Name)
	if err != nil {
		return errors.Wrap(err, "delete files from storage")
	}

	_, err = conn.BcpCollection().DeleteOne(ctx, bson.M{"name": bcp.Name})
	if err != nil {
		return errors.Wrap(err, "delete metadata from db")
	}

	return nil
}

func deleteIncremetalChainImpl(ctx context.Context, conn connect.Client, bcp *BackupMeta, node string) error {
	increments, err := FetchAllIncrements(ctx, conn, bcp)
	if err != nil {
		return err
	}

	err = CanDeleteIncrementalChain(ctx, conn, bcp, increments)
	if err != nil {
		return err
	}

	all := []*BackupMeta{bcp}
	for _, bcps := range increments {
		all = append(all, bcps...)
	}

	stg, err := util.StorageFromConfig(&bcp.Store.StorageConf, node, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	for i := len(all) - 1; i >= 0; i-- {
		bcp := all[i]

		err = DeleteBackupFiles(stg, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "delete files from storage")
		}

		_, err = conn.BcpCollection().DeleteOne(ctx, bson.M{"name": bcp.Name})
		if err != nil {
			return errors.Wrap(err, "delete metadata from db")
		}

	}

	return nil
}

func CanDeleteBackup(ctx context.Context, conn connect.Client, bcp *BackupMeta) error {
	if bcp.Status.IsRunning() {
		return ErrBackupInProgress
	}
	if !isValidBaseSnapshot(bcp) {
		return nil
	}
	if bcp.Type == defs.IncrementalBackup {
		return ErrIncrementalBackup
	}

	required, err := isRequiredForOplogSlicing(ctx, conn, bcp.LastWriteTS, primitive.Timestamp{})
	if err != nil {
		return errors.Wrap(err, "check pitr requirements")
	}
	if required {
		return ErrBaseForPITR
	}

	return nil
}

func CanDeleteIncrementalChain(
	ctx context.Context,
	conn connect.Client,
	base *BackupMeta,
	increments [][]*BackupMeta,
) error {
	if base.Status.IsRunning() {
		return ErrBackupInProgress
	}
	if base.Type != defs.IncrementalBackup {
		return ErrNonIncrementalBackup
	}
	if base.SrcBackup != "" {
		return ErrNotBaseIncrement
	}

	lastWrite := base.LastWriteTS
	for _, incs := range increments {
		for _, inc := range incs {
			if inc.Status == defs.StatusDone {
				lastWrite = inc.LastWriteTS
			}
		}
	}

	required, err := isRequiredForOplogSlicing(ctx, conn, lastWrite, base.LastWriteTS)
	if err != nil {
		return errors.Wrap(err, "check pitr requirements")
	}
	if required {
		return ErrBaseForPITR
	}

	return nil
}

func FetchAllIncrements(
	ctx context.Context,
	conn connect.Client,
	base *BackupMeta,
) ([][]*BackupMeta, error) {
	if base.SrcBackup != "" {
		return nil, ErrNotBaseIncrement
	}

	chain := [][]*BackupMeta{}

	lastInc := base
	for {
		cur, err := conn.BcpCollection().Find(ctx, bson.D{{"src_backup", lastInc.Name}})
		if err != nil {
			return nil, errors.Wrap(err, "query")
		}

		increments := []*BackupMeta{}
		if err := cur.All(ctx, &increments); err != nil {
			return nil, errors.Wrap(err, "decode")
		}
		if len(increments) == 0 {
			// no more increments
			break
		}

		chain = append(chain, increments)
		succ := &BackupMeta{}
		for _, inc := range increments {
			if inc.Status == defs.StatusDone {
				succ = inc
			}
		}
		if succ == nil {
			// no more successful increments (and no more following)
			break
		}

		lastInc = succ
	}

	return chain, nil
}

func isSourceForIncremental(
	ctx context.Context,
	conn connect.Client,
	bcpName string,
) (bool, error) {
	// check if there is an increment based on the backup
	f := bson.D{
		{"src_backup", bcpName},
		{"status", bson.M{"$nin": bson.A{defs.StatusCancelled, defs.StatusError}}},
	}
	res := conn.BcpCollection().FindOne(ctx, f)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// the backup is the last increment in the chain
			return false, nil
		}
		return false, errors.Wrap(err, "query")
	}

	return true, nil
}

func isValidBaseSnapshot(bcp *BackupMeta) bool {
	if bcp.Status != defs.StatusDone {
		return false
	}
	if bcp.Type == defs.ExternalBackup {
		return false
	}
	if util.IsSelective(bcp.Namespaces) {
		return false
	}

	return true
}

func isRequiredForOplogSlicing(
	ctx context.Context,
	conn connect.Client,
	lw primitive.Timestamp,
	baseLW primitive.Timestamp,
) (bool, error) {
	enabled, oplogOnly, err := config.IsPITREnabled(ctx, conn)
	if err != nil {
		return false, err
	}
	if !enabled || oplogOnly {
		return false, nil
	}

	nextRestoreTime, err := FindBaseSnapshotLWAfter(ctx, conn, lw)
	if err != nil {
		return false, errors.Wrap(err, "find next snapshot")
	}
	if !nextRestoreTime.IsZero() {
		// there is another valid base snapshot for oplog slicing
		return false, nil
	}

	prevRestoreTime, err := FindBaseSnapshotLWBefore(ctx, conn, lw, baseLW)
	if err != nil {
		return false, errors.Wrap(err, "find previous snapshot")
	}

	timelines, err := oplog.PITRTimelinesBetween(ctx, conn, prevRestoreTime, lw)
	if err != nil {
		return false, errors.Wrap(err, "get oplog range from previous backup")
	}
	// check if there is a gap (missed ops) in oplog range between previous and following backup restore_to time
	if len(timelines) != 1 || prevRestoreTime.T >= timelines[0].Start {
		return false, nil
	}

	return true, nil
}

// DeleteBackupBefore deletes backups which are older than given time
func DeleteBackupBefore(
	ctx context.Context,
	conn connect.Client,
	t time.Time,
	bcpType defs.BackupType,
	node string,
) error {
	backups, err := ListDeleteBackupBefore(ctx, conn, primitive.Timestamp{T: uint32(t.Unix())}, bcpType)
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		return nil
	}

	stg, err := util.GetStorage(ctx, conn, node, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	for i := range backups {
		bcp := &backups[i]

		err := DeleteBackupFiles(stg, bcp.Name)
		if err != nil {
			return errors.Wrapf(err, "delete files from storage for %q", bcp.Name)
		}

		_, err = conn.BcpCollection().DeleteOne(ctx, bson.M{"name": bcp.Name})
		if err != nil {
			return errors.Wrapf(err, "delete metadata from db for %q", bcp.Name)
		}
	}

	return nil
}

func ListDeleteBackupBefore(
	ctx context.Context,
	conn connect.Client,
	ts primitive.Timestamp,
	bcpType defs.BackupType,
) ([]BackupMeta, error) {
	info, err := MakeCleanupInfo(ctx, conn, ts)
	if err != nil {
		return nil, err
	}
	if len(info.Backups) == 0 {
		return nil, nil
	}
	if bcpType == "" {
		return info.Backups, nil
	}

	pred := func(m *BackupMeta) bool { return m.Type == bcpType }
	switch bcpType {
	case defs.LogicalBackup:
		pred = func(m *BackupMeta) bool {
			return m.Type == defs.LogicalBackup && !util.IsSelective(m.Namespaces)
		}
	case SelectiveBackup:
		pred = func(m *BackupMeta) bool { return util.IsSelective(m.Namespaces) }
	}

	rv := []BackupMeta{}
	for i := range info.Backups {
		if pred(&info.Backups[i]) {
			rv = append(rv, info.Backups[i])
		}
	}

	return rv, nil
}

func MakeCleanupInfo(ctx context.Context, conn connect.Client, ts primitive.Timestamp) (CleanupInfo, error) {
	backups, err := listBackupsBefore(ctx, conn, primitive.Timestamp{T: ts.T + 1})
	if err != nil {
		return CleanupInfo{}, errors.Wrap(err, "list backups before")
	}
	chunks, err := listChunksBefore(ctx, conn, ts)
	if err != nil {
		return CleanupInfo{}, errors.Wrap(err, "list chunks before")
	}
	if len(backups) == 0 {
		return CleanupInfo{Backups: backups, Chunks: chunks}, nil
	}

	if r := &backups[len(backups)-1]; r.LastWriteTS.T == ts.T {
		// there is a backup at the `ts`
		backups = backups[:len(backups)-1]

		if isValidBaseSnapshot(r) {
			// it can be used to fully restore data to the `ts` state.
			// no need to exclude any backups and chunks before the `ts`.
			// except increments that is base for following increment (after `ts`) must be excluded
			backups, err = extractLastIncrementalChain(ctx, conn, backups)
			if err != nil {
				return CleanupInfo{}, errors.Wrap(err, "extract last incremental chain")
			}

			beforeChunks := make([]oplog.OplogChunk, 0, len(chunks))
			for _, chunk := range chunks {
				if !chunk.EndTS.After(r.LastWriteTS) {
					beforeChunks = append(beforeChunks, chunk)
				}
			}
			chunks = beforeChunks

			return CleanupInfo{Backups: backups, Chunks: chunks}, nil
		}
	}

	baseIndex := len(backups) - 1
	for ; baseIndex != -1; baseIndex-- {
		if isValidBaseSnapshot(&backups[baseIndex]) {
			break
		}
	}
	if baseIndex == -1 {
		// no valid base snapshot to exclude

		beforeChunks := make([]oplog.OplogChunk, 0, len(chunks))
		for _, chunk := range chunks {
			if !chunk.EndTS.After(ts) {
				beforeChunks = append(beforeChunks, chunk)
			}
		}
		chunks = beforeChunks

		return CleanupInfo{Backups: backups, Chunks: chunks}, nil
	}

	beforeChunks := []oplog.OplogChunk{}
	afterChunks := []oplog.OplogChunk{}
	for _, chunk := range chunks {
		if !chunk.EndTS.After(backups[baseIndex].LastWriteTS) {
			beforeChunks = append(beforeChunks, chunk)
		} else {
			// keep chunks after the last base snapshot restore time
			// the backup may be excluded
			afterChunks = append(afterChunks, chunk)
		}
	}

	if oplog.HasSingleTimelineToCover(afterChunks, backups[baseIndex].LastWriteTS.T, ts.T) {
		// there is single continuous oplog range between snapshot and ts.
		// keep the backup and chunks to be able to restore to the ts
		copy(backups[baseIndex:], backups[baseIndex+1:])
		backups = backups[:len(backups)-1]
		chunks = beforeChunks
	} else {
		// no chunks yet but if PITR is ON, the backup can be base snapshot for it
		enabled, oplogOnly, err := config.IsPITREnabled(ctx, conn)
		if err != nil {
			return CleanupInfo{}, errors.Wrap(err, "get PITR status")
		}
		if enabled && !oplogOnly {
			nextRestoreTime, err := FindBaseSnapshotLWAfter(ctx, conn, ts)
			if err != nil {
				return CleanupInfo{}, errors.Wrap(err, "find next snapshot")
			}

			if nextRestoreTime.IsZero() {
				// it is not the last base snapshot for PITR
				copy(backups[baseIndex:], backups[baseIndex+1:])
				backups = backups[:len(backups)-1]
				chunks = beforeChunks
			}
		}
	}

	// exclude increments that is base for following increment (after `ts`)
	backups, err = extractLastIncrementalChain(ctx, conn, backups)
	if err != nil {
		return CleanupInfo{}, errors.Wrap(err, "extract last incremental chain")
	}

	return CleanupInfo{Backups: backups, Chunks: chunks}, nil
}

// listBackupsBefore returns backups with restore cluster time less than or equals to ts.
//
// It does not include backups stored on an external storages.
func listBackupsBefore(ctx context.Context, conn connect.Client, ts primitive.Timestamp) ([]BackupMeta, error) {
	f := bson.D{
		{"store.profile", nil},
		{"last_write_ts", bson.M{"$lt": ts}},
		{"status", bson.M{"$in": bson.A{
			defs.StatusDone,
			defs.StatusCancelled,
			defs.StatusError,
		}}},
	}
	o := options.Find().SetSort(bson.D{{"last_write_ts", 1}})
	cur, err := conn.BcpCollection().Find(ctx, f, o)
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	rv := []BackupMeta{}
	err = cur.All(ctx, &rv)
	return rv, errors.Wrap(err, "cursor: all")
}

// listChunksBefore returns oplog chunks that contain an op at the ts
func listChunksBefore(ctx context.Context, conn connect.Client, ts primitive.Timestamp) ([]oplog.OplogChunk, error) {
	f := bson.D{{"start_ts", bson.M{"$lt": ts}}}
	o := options.Find().SetSort(bson.D{{"start_ts", 1}})
	cur, err := conn.PITRChunksCollection().Find(ctx, f, o)
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	rv := []oplog.OplogChunk{}
	err = cur.All(ctx, &rv)
	return rv, errors.Wrap(err, "cursor: all")
}

func extractLastIncrementalChain(
	ctx context.Context,
	conn connect.Client,
	bcps []BackupMeta,
) ([]BackupMeta, error) {
	// lookup for the last incremental
	i := len(bcps) - 1
	for ; i != -1; i-- {
		if bcps[i].Status != defs.StatusDone {
			continue
		}
		if bcps[i].Type == defs.IncrementalBackup {
			break
		}
	}
	if i == -1 {
		// not found
		return bcps, nil
	}

	isSource, err := isSourceForIncremental(ctx, conn, bcps[i].Name)
	if err != nil {
		return bcps, err
	}
	if !isSource {
		return bcps, nil
	}

	return extractIncrementalChain(bcps, i), nil
}

func extractIncrementalChain(bcps []BackupMeta, i int) []BackupMeta {
	for base := bcps[i].Name; i != -1; i-- {
		if bcps[i].Status != defs.StatusDone {
			continue
		}
		if bcps[i].Name != base {
			continue
		}
		base = bcps[i].SrcBackup

		// exclude the backup from slice by index
		copy(bcps[i:], bcps[i+1:])
		bcps = bcps[:len(bcps)-1]

		if base == "" {
			// the root/base of the chain
			break
		}
	}

	return bcps
}
