package restore

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mongodb/mongo-tools/common/db"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/restore/phys"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func GetPhysRestoreMeta(restoreName string, stg storage.Storage, l log.LogEvent) (*RestoreMeta, error) {
	mjson := filepath.Join(defs.PhysRestoresDir, restoreName) + ".json"
	_, err := stg.FileStat(mjson)
	if err != nil && !errors.Is(err, storage.ErrNotExist) {
		return nil, errors.Wrapf(err, "get file %s", mjson)
	}

	var rmeta *RestoreMeta
	if err == nil {
		src, err := stg.SourceReader(mjson)
		if err != nil {
			return nil, errors.Wrapf(err, "get file %s", mjson)
		}

		err = json.NewDecoder(src).Decode(&rmeta)
		if err != nil {
			return nil, errors.Wrapf(err, "decode meta %s", mjson)
		}
	}

	condsm, err := ParsePhysRestoreStatus(restoreName, stg, l)
	if err != nil {
		return rmeta, errors.Wrap(err, "parse physical restore status")
	}

	if rmeta == nil {
		return condsm, err
	}

	rmeta.Replsets = condsm.Replsets
	if condsm.Status != "" {
		rmeta.Status = condsm.Status
	}
	rmeta.LastTransitionTS = condsm.LastTransitionTS
	if condsm.Error != "" {
		rmeta.Error = condsm.Error
	}
	rmeta.Hb = condsm.Hb
	rmeta.Conditions = condsm.Conditions
	rmeta.Type = defs.PhysicalBackup
	rmeta.Stat = condsm.Stat

	return rmeta, err
}

// ParsePhysRestoreStatus parses phys restore's sync files and creates RestoreMeta.
//
// On files format, see comments for *PhysRestore.toState() in pbm/restore/physical.go
func ParsePhysRestoreStatus(restoreName string, stg storage.Storage, l log.LogEvent) (*RestoreMeta, error) {
	rfiles, err := stg.List(defs.PhysRestoresDir+"/"+restoreName, "")
	if err != nil {
		return nil, errors.Wrap(err, "get files")
	}

	meta := RestoreMeta{Name: restoreName, Type: defs.PhysicalBackup}

	rss := make(map[string]struct {
		rs    RestoreReplset
		nodes map[string]RestoreNode
	})

	for _, f := range rfiles {
		parts := strings.SplitN(f.Name, ".", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "rs":
			rsparts := strings.Split(parts[1], "/")

			if len(rsparts) < 2 {
				continue
			}

			rsName := strings.TrimPrefix(rsparts[0], "rs.")
			rs, ok := rss[rsName]
			if !ok {
				rs.rs.Name = rsName
				rs.nodes = make(map[string]RestoreNode)
			}

			p := strings.Split(rsparts[1], ".")

			if len(p) < 2 {
				continue
			}
			switch p[0] {
			case "node":
				if len(p) < 3 {
					continue
				}
				nName := strings.Join(p[1:len(p)-1], ".")
				node, ok := rs.nodes[nName]
				if !ok {
					node.Name = nName
				}
				cond, err := parsePhysRestoreCond(stg, f.Name, restoreName)
				if err != nil {
					return nil, err
				}
				if cond.Status == "hb" {
					node.Hb.T = uint32(cond.Timestamp)
				} else {
					node.Conditions.Insert(cond)
					l := node.Conditions[len(node.Conditions)-1]
					node.Status = l.Status
					node.LastTransitionTS = l.Timestamp
					node.Error = l.Error
				}

				rs.nodes[nName] = node
			case "rs":
				if p[1] == "txn" {
					continue
				}
				if p[1] == "partTxn" {
					src, err := stg.SourceReader(filepath.Join(defs.PhysRestoresDir, restoreName, f.Name))
					if err != nil {
						l.Error("get partial txn file %s: %v", f.Name, err)
						break
					}

					ops := []db.Oplog{}
					err = json.NewDecoder(src).Decode(&ops)
					if err != nil {
						l.Error("unmarshal partial txn %s: %v", f.Name, err)
						break
					}
					rs.rs.PartialTxn = append(rs.rs.PartialTxn, ops...)
					rss[rsName] = rs
					continue
				}

				cond, err := parsePhysRestoreCond(stg, f.Name, restoreName)
				if err != nil {
					return nil, err
				}
				if cond.Status == "hb" {
					rs.rs.Hb.T = uint32(cond.Timestamp)
				} else {
					rs.rs.Conditions.Insert(cond)
					l := rs.rs.Conditions[len(rs.rs.Conditions)-1]
					rs.rs.Status = l.Status
					rs.rs.LastTransitionTS = l.Timestamp
					rs.rs.Error = l.Error
				}
			case "stat":
				src, err := stg.SourceReader(filepath.Join(defs.PhysRestoresDir, restoreName, f.Name))
				if err != nil {
					l.Error("get stat file %s: %v", f.Name, err)
					break
				}
				if meta.Stat == nil {
					meta.Stat = &phys.RestoreStat{RS: make(map[string]map[string]phys.RestoreRSMetrics)}
				}
				st := phys.RestoreShardStat{}
				err = json.NewDecoder(src).Decode(&st)
				if err != nil {
					l.Error("unmarshal stat file %s: %v", f.Name, err)
					break
				}
				if _, ok := meta.Stat.RS[rsName]; !ok {
					meta.Stat.RS[rsName] = make(map[string]phys.RestoreRSMetrics)
				}
				nName := strings.Join(p[1:], ".")
				lstat := meta.Stat.RS[rsName][nName]
				lstat.DistTxn.Partial += st.Txn.Partial
				lstat.DistTxn.ShardUncommitted += st.Txn.ShardUncommitted
				lstat.DistTxn.LeftUncommitted += st.Txn.LeftUncommitted
				if st.D != nil {
					lstat.Download = *st.D
				}
				meta.Stat.RS[rsName][nName] = lstat
			}
			rss[rsName] = rs

		case "cluster":
			cond, err := parsePhysRestoreCond(stg, f.Name, restoreName)
			if err != nil {
				return nil, err
			}

			if cond.Status == "hb" {
				meta.Hb.T = uint32(cond.Timestamp)
			} else {
				meta.Conditions.Insert(cond)
				lstat := meta.Conditions[len(meta.Conditions)-1]
				meta.Status = lstat.Status
				meta.LastTransitionTS = lstat.Timestamp
				meta.Error = lstat.Error
			}
		}
	}

	// If all nodes in the rs are in "error" state, set rs as "error".
	// We have "partlyDone", so it's not an error if at least one node is "done".
	for _, rs := range rss {
		noerr := 0
		nodeErr := ""
		for _, node := range rs.nodes {
			rs.rs.Nodes = append(rs.rs.Nodes, node)
			if node.Status != defs.StatusError {
				noerr++
			}
			if node.Error != "" {
				nodeErr = node.Error
			}
		}
		if noerr == 0 {
			rs.rs.Status = defs.StatusError
			if rs.rs.Error == "" {
				rs.rs.Error = nodeErr
			}
			meta.Status = defs.StatusError
			if meta.Error == "" {
				meta.Error = nodeErr
			}
		}
		meta.Replsets = append(meta.Replsets, rs.rs)
	}

	return &meta, nil
}

func parsePhysRestoreCond(stg storage.Storage, fname, restoreName string) (*Condition, error) {
	s := strings.Split(fname, ".")
	cond := Condition{Status: defs.Status(s[len(s)-1])}

	src, err := stg.SourceReader(filepath.Join(defs.PhysRestoresDir, restoreName, fname))
	if err != nil {
		return nil, errors.Wrapf(err, "get file %s", fname)
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, errors.Wrapf(err, "read file %s", fname)
	}

	if cond.Status == defs.StatusError || cond.Status == defs.StatusExtTS {
		estr := strings.SplitN(string(b), ":", 2)
		if len(estr) != 2 {
			return nil, errors.Errorf("malformatted data in %s: %s", fname, b)
		}
		cond.Timestamp, err = strconv.ParseInt(estr[0], 10, 0)
		if err != nil {
			return nil, errors.Wrapf(err, "read ts from %s", fname)
		}
		if cond.Status == defs.StatusError {
			cond.Error = estr[1]
		}
		return &cond, nil
	}

	cond.Timestamp, err = strconv.ParseInt(string(b), 10, 0)
	if err != nil {
		return nil, errors.Wrapf(err, "read ts from %s", fname)
	}

	return &cond, nil
}
