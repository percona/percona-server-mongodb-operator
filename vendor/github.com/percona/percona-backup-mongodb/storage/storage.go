package storage

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/percona/percona-backup-mongodb/internal/utils"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

type Credentials struct {
	AccessKeyID     string `yaml:"access-key-id"`
	SecretAccessKey string `yaml:"secret-access-key"`
	Vault           struct {
		Server string `yaml:"server"`
		Secret string `yaml:"secret"`
		Token  string `yaml:"token"`
	} `yaml:"vault"`
}

type S3 struct {
	Region      string      `yaml:"region"`
	EndpointURL string      `yaml:"endpointUrl"`
	Bucket      string      `yaml:"bucket"`
	Credentials Credentials `yaml:"credentials"`
}

type Filesystem struct {
	Path string `yaml:"path"`
}

type Storage struct {
	Type       string     `yaml:"type"`
	S3         S3         `yaml:"s3,omitempty"`
	Filesystem Filesystem `yaml:"filesystem"`
}

type StorageInfo struct {
	Type       string
	S3         S3
	Filesystem Filesystem
}

type Storages struct {
	Storages   map[string]Storage
	lastUpdate time.Time
	filename   string
}

func (s Storage) Info() StorageInfo {
	return StorageInfo{
		Type: s.Type,
		S3: S3{
			Region:      s.S3.Region,
			EndpointURL: s.S3.EndpointURL,
			Bucket:      s.S3.Bucket,
		},
		Filesystem: Filesystem{
			Path: s.Filesystem.Path,
		},
	}
}

func NewStorageBackends(buf []byte) (*Storages, error) {
	s := &Storages{
		Storages: make(map[string]Storage),
	}

	if err := s.parse(buf); err != nil {
		return nil, errors.Wrapf(err, "cannot unmarshal input: %s", err)
	}

	return s, nil
}

func NewStorageBackendsFromYaml(filename string) (*Storages, error) {
	filename = utils.Expand(filename)

	s := &Storages{
		filename: filename,
		Storages: make(map[string]Storage),
	}

	if err := s.load(); err != nil {
		return nil, errors.Wrapf(err, "cannot unmarshal yaml file %s: %s", filename, err)
	}

	return s, nil
}

func (s *Storages) Reload() error {
	// if it was created from a bytes array ...
	if s.filename == "" {
		return nil
	}
	si, err := os.Stat(s.filename)
	if err != nil {
		return errors.Wrapf(err, "cannot open file %s", s.filename)
	}

	if !si.ModTime().After(s.lastUpdate) {
		return nil
	}

	s.lastUpdate = si.ModTime()
	return s.load()
}

func (s *Storages) load() error {
	// if it was created from a bytes array ...
	if s.filename == "" {
		return nil
	}
	si, err := os.Stat(s.filename)
	if err != nil {
		return errors.Wrapf(err, "cannot open file %s", s.filename)
	}

	s.lastUpdate = si.ModTime()

	buf, err := ioutil.ReadFile(filepath.Clean(s.filename))
	if err != nil {
		return errors.Wrap(err, "cannot load configuration from file")
	}

	return s.parse(buf)
}

func (s *Storages) parse(buf []byte) error {
	s.Storages = make(map[string]Storage)

	if err := yaml.Unmarshal(buf, s.Storages); err != nil {
		return errors.Wrapf(err, "cannot unmarshal yaml file %s: %s", s.filename, err)
	}
	return nil
}

func (s Storages) Exists(name string) bool {
	_, ok := s.Storages[name]
	return ok
}

func (s Storages) Get(name string) (Storage, error) {
	storage, ok := s.Storages[name]
	if !ok {
		return Storage{}, fmt.Errorf("Storage %q doesn't exists", name)
	}
	return storage, nil
}

func (s Storages) StorageInfo(name string) (StorageInfo, error) {
	si, ok := s.Storages[name]
	if !ok {
		return StorageInfo{}, fmt.Errorf("Storage name %s doesn't exists", name)
	}
	return si.Info(), nil
}

func (s Storages) StoragesInfo() map[string]StorageInfo {
	si := make(map[string]StorageInfo)
	for name, storage := range s.Storages {
		si[name] = storage.Info()
	}
	return si
}
