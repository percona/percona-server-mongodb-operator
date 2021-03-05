package fs

import (
	"io"
	"io/ioutil"
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
		return inf, errors.New("file empty")
	}

	return inf, nil
}

func (fs *FS) List(prefix string) ([]storage.FileInfo, error) {
	var files []storage.FileInfo

	prefix = path.Join(fs.opts.Path, prefix)

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
			files = append(files, storage.FileInfo{Name: f, Size: info.Size()})
		}

		return nil
	})

	return files, err
}

func (fs *FS) Files(suffix string) ([][]byte, error) {
	files, err := ioutil.ReadDir(fs.opts.Path)
	if err != nil {
		return nil, errors.Wrap(err, "read dir")
	}

	var bcps [][]byte
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), suffix) {
			continue
		}

		fpath := path.Join(fs.opts.Path, f.Name())
		data, err := ioutil.ReadFile(fpath)
		if err != nil {
			return nil, errors.Wrapf(err, "read file '%s'", fpath)
		}

		bcps = append(bcps, data)
	}

	return bcps, nil
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
