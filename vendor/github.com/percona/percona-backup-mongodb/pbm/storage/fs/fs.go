package fs

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func IsRetryableError(err error) bool {
	var e *RetryableError
	return errors.As(err, &e)
}

const tmpFileSuffix = ".tmp"

type Config struct {
	Path string `bson:"path" json:"path" yaml:"path"`
}

func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	return &Config{Path: cfg.Path}
}

func (cfg *Config) Equal(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	return cfg.Path == other.Path
}

func (cfg *Config) Cast() error {
	if cfg.Path == "" {
		return errors.New("path can't be empty")
	}

	return nil
}

type FS struct {
	root string
}

func New(opts *Config) (*FS, error) {
	info, err := os.Lstat(opts.Path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(opts.Path, os.ModeDir|0o755); err != nil {
				return nil, errors.Wrapf(err, "mkdir %s", opts.Path)
			}

			return &FS{opts.Path}, nil
		}

		return nil, errors.Wrapf(err, "stat %s", opts.Path)
	}

	root := opts.Path
	if info.Mode()&os.ModeSymlink != 0 {
		root, err = filepath.EvalSymlinks(opts.Path)
		if err != nil {
			return nil, errors.Wrapf(err, "resolve link: %s", opts.Path)
		}
		info, err = os.Lstat(root)
		if err != nil {
			return nil, errors.Wrapf(err, "stat %s", root)
		}
	}
	if !info.Mode().IsDir() {
		return nil, errors.Errorf("%s is not directory", root)
	}

	return &FS{root}, nil
}

func (*FS) Type() storage.Type {
	return storage.Filesystem
}

//nolint:nonamedreturns
func writeSync(finalpath string, data io.Reader) (err error) {
	filepath := finalpath + tmpFileSuffix

	err = os.MkdirAll(path.Dir(filepath), os.ModeDir|0o755)
	if err != nil {
		return errors.Wrapf(err, "create path %s", path.Dir(filepath))
	}

	fw, err := os.Create(filepath)
	if err != nil {
		return errors.Wrapf(err, "create destination file <%s>", filepath)
	}
	defer func() {
		if err != nil {
			if fw != nil {
				fw.Close()
			}

			if os.IsNotExist(err) {
				err = &RetryableError{Err: err}
			} else {
				os.Remove(filepath)
			}

		}
	}()

	err = os.Chmod(filepath, 0o644)
	if err != nil {
		return errors.Wrapf(err, "change permissions for file <%s>", filepath)
	}

	_, err = io.Copy(fw, data)
	if err != nil {
		return errors.Wrapf(err, "copy file <%s>", filepath)
	}

	err = fw.Sync()
	if err != nil {
		return errors.Wrapf(err, "sync file <%s>", filepath)
	}

	err = fw.Close()
	if err != nil {
		return errors.Wrapf(err, "close file <%s>", filepath)
	}
	fw = nil

	err = os.Rename(filepath, finalpath)
	if err != nil {
		return err
	}

	return nil
}

func (fs *FS) Save(name string, data io.Reader, _ int64) error {
	return writeSync(path.Join(fs.root, name), data)
}

func (fs *FS) SourceReader(name string) (io.ReadCloser, error) {
	filepath := path.Join(fs.root, name)
	fr, err := os.Open(filepath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, storage.ErrNotExist
	}
	return fr, errors.Wrapf(err, "open file '%s'", filepath)
}

func (fs *FS) FileStat(name string) (storage.FileInfo, error) {
	inf := storage.FileInfo{}

	f, err := os.Stat(path.Join(fs.root, name))
	if errors.Is(err, os.ErrNotExist) {
		return inf, storage.ErrNotExist
	}
	if err != nil {
		return inf, err
	}

	inf.Size = f.Size()

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}

	return inf, nil
}

func (fs *FS) List(prefix, suffix string) ([]storage.FileInfo, error) {
	var files []storage.FileInfo

	base := filepath.Join(fs.root, prefix)
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errors.Wrap(err, "walking the path")
		}

		info, _ := entry.Info()
		if info.IsDir() {
			return nil
		}

		f := filepath.ToSlash(strings.TrimPrefix(path, base))
		if len(f) == 0 {
			return nil
		}
		if f[0] == '/' {
			f = f[1:]
		}

		// ignore temp file unless it is not requested explicitly
		if suffix == "" && strings.HasSuffix(f, tmpFileSuffix) {
			return nil
		}
		if strings.HasSuffix(f, suffix) {
			files = append(files, storage.FileInfo{Name: f, Size: info.Size()})
		}
		return nil
	})

	return files, err
}

func (fs *FS) Copy(src, dst string) error {
	from, err := os.Open(path.Join(fs.root, src))
	if err != nil {
		return errors.Wrap(err, "open src")
	}

	return writeSync(path.Join(fs.root, dst), from)
}

// Delete deletes given file from FS.
// It returns storage.ErrNotExist if a file isn't exists
func (fs *FS) Delete(name string) error {
	err := os.RemoveAll(path.Join(fs.root, name))
	if os.IsNotExist(err) {
		return storage.ErrNotExist
	}
	return err
}
