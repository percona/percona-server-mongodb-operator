package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/sdk"
)

type diagnosticOptions struct {
	path    string
	opid    string
	name    string
	archive bool
}

func handleDiagnostic(
	ctx context.Context,
	pbm *sdk.Client,
	opts diagnosticOptions,
) (fmt.Stringer, error) {
	if opts.opid == "" && opts.name == "" {
		return nil, errors.New("--opid or --name must be provided")
	}

	prefix := opts.opid
	if opts.opid == "" {
		cid, err := sdk.FindCommandIDByName(ctx, pbm, opts.name)
		if err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				// after physical restore, command and log collections are empty.
				rst, err := pbm.GetRestoreByName(ctx, opts.name)
				if err == nil && rst.Type != sdk.LogicalBackup {
					return nil, errors.New("PBM does not support reporting for physical restores")
				}
				return nil, errors.New("command not found")
			}
			return nil, errors.Wrap(err, "find opid by name")
		}
		opts.opid = string(cid)
		prefix = opts.name
	}

	report, err := sdk.Diagnostic(ctx, pbm, sdk.CommandID(opts.opid))
	if err != nil {
		return nil, err
	}

	if fileInfo, err := os.Stat(opts.path); err != nil {
		if !os.IsNotExist(err) {
			return nil, errors.Wrap(err, "stat")
		}
		err = os.MkdirAll(opts.path, 0o777)
		if err != nil {
			return nil, errors.Wrap(err, "create path")
		}
	} else if !fileInfo.IsDir() {
		return nil, errors.Errorf("%s is not a dir", opts.path)
	}

	err = writeToFile(opts.path, prefix+".report.json", report)
	if err != nil {
		return nil, errors.Wrapf(err,
			"failed to save %s", filepath.Join(opts.path, prefix+".report.json"))
	}

	var cmdType sdk.CommandType
	if report.Command != nil {
		cmdType = report.Command.Cmd
	}
	switch cmdType {
	case sdk.CmdBackup:
		meta, err := pbm.GetBackupByOpID(ctx, opts.opid, sdk.GetBackupByNameOptions{})
		if err != nil {
			if !errors.Is(err, sdk.ErrNotFound) {
				return nil, errors.Wrap(err, "get backup meta")
			}
		} else {
			meta.Store = backup.Storage{}
			err = writeToFile(opts.path, prefix+".backup.json", meta)
			if err != nil {
				return nil, errors.Wrapf(err,
					"failed to save %s", filepath.Join(opts.path, prefix+".backup.json"))
			}
		}
	case sdk.CmdRestore:
		meta, err := pbm.GetRestoreByOpID(ctx, opts.opid)
		if err != nil {
			if !errors.Is(err, sdk.ErrNotFound) {
				return nil, errors.Wrap(err, "get restore meta")
			}
		} else {
			err = writeToFile(opts.path, prefix+".restore.json", meta)
			if err != nil {
				return nil, errors.Wrapf(err,
					"failed to save %s", filepath.Join(opts.path, prefix+".restore.json"))
			}

			meta, err := pbm.GetBackupByName(ctx, meta.Backup, sdk.GetBackupByNameOptions{})
			if err != nil {
				if !errors.Is(err, sdk.ErrNotFound) {
					return nil, errors.Wrap(err, "get backup meta")
				}
			} else {
				meta.Store = backup.Storage{}
				err = writeToFile(opts.path, prefix+".backup.json", meta)
				if err != nil {
					return nil, errors.Wrapf(err,
						"failed to save %s", filepath.Join(opts.path, prefix+".backup.json"))
				}
			}
		}
	}

	err = writeLogToFile(ctx, pbm, opts.path, prefix, opts.opid)
	if err != nil {
		return nil, errors.Wrap(err, "failed to save command log")
	}

	if opts.archive {
		err = createArchive(opts.path, prefix)
		if err != nil {
			return nil, errors.Wrap(err, "create archive")
		}
	}

	return outMsg{"Report is successfully created"}, nil
}

//nolint:nonamedreturns
func writeLogToFile(ctx context.Context, pbm *sdk.Client, path, prefix, opid string) (err error) {
	filename := filepath.Join(path, prefix+".log")
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(filename)
		}
	}()

	cur, err := sdk.CommandLogCursor(ctx, pbm, sdk.CommandID(opid))
	if err != nil {
		return errors.Wrap(err, "open log cursor")
	}
	defer cur.Close(ctx)

	eol := []byte("\n")
	for cur.Next(ctx) {
		rec, err := cur.Record()
		if err != nil {
			return errors.Wrap(err, "log: decode")
		}

		s := rec.Stringify(log.AsUTC, true, true)
		n, err := file.WriteString(s)
		if err != nil {
			return errors.Wrap(err, "log: write")
		}
		if n != len(s) {
			return errors.Wrap(io.ErrShortWrite, "log")
		}

		n, err = file.Write(eol)
		if err != nil {
			return errors.Wrap(err, "log: write")
		}
		if n != len(eol) {
			return errors.Wrap(io.ErrShortWrite, "log")
		}
	}

	err = cur.Err()
	if err != nil {
		return errors.Wrap(err, "log cursor")
	}

	err = file.Close()
	if err != nil {
		return errors.Wrap(err, "failed to save file command.log")
	}

	return nil
}

func writeToFile(dirname, name string, val any) error {
	data, err := bson.MarshalExtJSONIndent(val, true, true, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	file, err := os.Create(filepath.Join(dirname, name))
	if err != nil {
		return err
	}
	defer file.Close()

	n, err := file.Write(data)
	if err != nil {
		return errors.Wrap(err, "write")
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	err = file.Close()
	if err != nil {
		return errors.Wrap(err, "close file")
	}

	return nil
}

func createArchive(path, prefix string) error {
	file, err := os.CreateTemp("", "")
	if err != nil {
		return errors.Wrap(err, "create tmp file")
	}
	defer func() {
		if file != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()

	archive := zip.NewWriter(file)
	err = archive.AddFS(os.DirFS(path))
	if err != nil {
		return err
	}

	err = archive.Close()
	if err != nil {
		return errors.Wrap(err, "close zip")
	}

	err = file.Close()
	if err != nil {
		return errors.Wrap(err, "close file")
	}

	err = os.Rename(file.Name(), filepath.Join(path, prefix+".zip"))
	if err != nil {
		return err
	}

	file = nil
	return nil
}
