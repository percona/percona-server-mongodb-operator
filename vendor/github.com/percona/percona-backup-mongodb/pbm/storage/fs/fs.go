package fs

import (
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
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

func (fs *FS) Save(name string, data io.Reader) error {
	filepath := path.Join(fs.opts.Path, name)
	fw, err := os.Create(filepath)
	if err != nil {
		return errors.Wrapf(err, "create destination file <%s>", filepath)
	}
	_, err = io.Copy(fw, data)
	return errors.Wrap(err, "write to file")
}

func (fs *FS) SourceReader(name string) (io.ReadCloser, error) {
	filepath := path.Join(fs.opts.Path, name)
	fr, err := os.Open(filepath)
	return fr, errors.Wrapf(err, "open file '%s'", filepath)
}

func (fs *FS) FilesList(suffix string) ([][]byte, error) {
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

func (fs *FS) Delete(name string) error {
	return os.Remove(path.Join(fs.opts.Path, name))
}
