package fs

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Conf struct {
	Path string `bson:"path" json:"path" yaml:"path"`
}

func (c *Conf) Cast() error {
	if c.Path == "" {
		return errors.New("path can't be empty")
	}

	return nil
}

type FS struct {
	opts Conf
}

func New(opts Conf) *FS {
	return &FS{
		opts: opts,
	}
}

func (fs *FS) Save(name string, data io.Reader, _ int) error {
	filepath := path.Join(fs.opts.Path, name)

	err := os.MkdirAll(path.Dir(filepath), os.ModeDir|0775)
	if err != nil {
		return errors.Wrapf(err, "create path %s", path.Dir(filepath))
	}

	fw, err := os.Create(filepath)
	if err != nil {
		return errors.Wrapf(err, "create destination file <%s>", filepath)
	}
	err = os.Chmod(filepath, 0664)
	if err != nil {
		return errors.Wrapf(err, "change permissions for file <%s>", filepath)
	}

	_, err = io.Copy(fw, data)
	return errors.Wrap(err, "write to file")
}

func (fs *FS) SourceReader(name string) (io.ReadCloser, error) {
	filepath := path.Join(fs.opts.Path, name)
	fr, err := os.Open(filepath)
	return fr, errors.Wrapf(err, "open file '%s'", filepath)
}

func (fs *FS) FileStat(name string) (inf storage.FileInfo, err error) {
	f, err := os.Stat(path.Join(fs.opts.Path, name))

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

	prefix = filepath.Join(fs.opts.Path, prefix)

	err := filepath.Walk(prefix, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errors.Wrap(err, "walking the path")
		}
		if !info.IsDir() {
			f := strings.TrimPrefix(path, prefix)
			f = filepath.ToSlash(f)
			if len(f) == 0 {
				return nil
			}
			if f[0] == '/' {
				f = f[1:]
			}
			if strings.HasSuffix(f, suffix) {
				files = append(files, storage.FileInfo{Name: f, Size: info.Size()})
			}
		}

		return nil
	})

	return files, err
}

func (fs *FS) Copy(src, dst string) error {
	from, err := os.Open(path.Join(fs.opts.Path, src))
	if err != nil {
		return errors.Wrap(err, "open src")
	}
	to, err := os.Create(path.Join(fs.opts.Path, dst))
	if err != nil {
		return errors.Wrap(err, "create dst")
	}
	_, err = io.Copy(to, from)
	return err
}

// Delete deletes given file from FS.
// It returns storage.ErrNotExist if a file isn't exists
func (fs *FS) Delete(name string) error {
	err := os.Remove(path.Join(fs.opts.Path, name))
	if os.IsNotExist(err) {
		return storage.ErrNotExist
	}
	return err
}
