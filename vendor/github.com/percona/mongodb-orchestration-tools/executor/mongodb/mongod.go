// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mongodb

import (
	"errors"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/percona/mongodb-orchestration-tools/internal"
	"github.com/percona/mongodb-orchestration-tools/internal/command"
	log "github.com/sirupsen/logrus"
	mongoConfig "github.com/timvaillancourt/go-mongodb-config/config"
)

const (
	DefaultDirMode                = os.FileMode(0700)
	DefaultKeyMode                = os.FileMode(0400)
	minWiredTigerCacheSizeGB      = 0.25
	gigaByte                 uint = 1024 * 1024 * 1024
)

func mkdir(path string, uid int, gid int, mode os.FileMode) error {
	if _, err := os.Stat(path); err != nil {
		err = os.Mkdir(path, mode)
		if err != nil {
			return err
		}
	}
	return os.Chown(path, uid, gid)
}

type Mongod struct {
	sync.Mutex
	config     *Config
	configFile string
	commandBin string
	command    *command.Command
	procState  chan *os.ProcessState
}

func NewMongod(config *Config, procState chan *os.ProcessState) *Mongod {
	return &Mongod{
		config:     config,
		configFile: filepath.Join(config.ConfigDir, "mongod.conf"),
		commandBin: filepath.Join(config.BinDir, "mongod"),
		procState:  procState,
	}
}

// The WiredTiger internal cache, by default, will use the larger of either 50% of
// (RAM - 1 GB), or 256 MB. For example, on a system with a total of 4GB of RAM the
// WiredTiger cache will use 1.5GB of RAM (0.5 * (4 GB - 1 GB) = 1.5 GB).
//
// https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB
//
func (m *Mongod) getWiredTigerCacheSizeGB() float64 {
	limitBytes := m.config.TotalMemoryMB * 1024 * 1024
	size := math.Floor(m.config.WiredTigerCacheRatio * float64(limitBytes-gigaByte))
	sizeGB := size / float64(gigaByte)
	if sizeGB < minWiredTigerCacheSizeGB {
		sizeGB = minWiredTigerCacheSizeGB
	}
	return sizeGB
}

// monitorMongodCommand() waits for the mongod command to be killed or exit,
// returning the *os.ProcessState of the completed process over the procState
// channel
func (m *Mongod) monitorMongodCommand() {
	state, err := m.command.Wait()
	if err != nil {
		log.Errorf("Error receiving mongod exit-state: %s", err)
		return
	}
	m.procState <- state
}

func (m *Mongod) Name() string {
	return "mongod"
}

func (m *Mongod) loadConfig() (*mongoConfig.Config, error) {
	log.WithFields(log.Fields{
		"config": m.configFile,
	}).Info("Loading mongodb config file")

	config, err := mongoConfig.Load(m.configFile)
	if err != nil {
		log.Errorf("Error loading mongodb configuration: %s", err)
		return nil, err
	}
	return config, err
}

func (m *Mongod) processMongodConfig(config *mongoConfig.Config) error {
	if config.Security == nil || config.Security.KeyFile == "" || config.Storage == nil || config.Storage.DbPath == "" {
		return errors.New("mongodb config file is not valid, must have security.keyFile and storage.dbPath defined!")
	}

	if config.Storage.Engine == "wiredTiger" {
		err := m.processWiredTigerConfig(config)
		if err != nil {
			log.Errorf("Error processing wiredTiger configuration: %s", err)
			return err
		}
	}
	return nil
}

func (m *Mongod) processWiredTigerConfig(config *mongoConfig.Config) error {
	cacheSizeGB := m.getWiredTigerCacheSizeGB()

	log.WithFields(log.Fields{
		"size_gb": cacheSizeGB,
		"ratio":   m.config.WiredTigerCacheRatio,
	}).Infof("Setting WiredTiger cache size")

	if config.Storage.WiredTiger == nil {
		config.Storage.WiredTiger = &mongoConfig.StorageWiredTiger{}
	}
	if config.Storage.WiredTiger.EngineConfig == nil {
		config.Storage.WiredTiger.EngineConfig = &mongoConfig.StorageWiredTigerEngineConfig{}
	}
	config.Storage.WiredTiger.EngineConfig.CacheSizeGB = cacheSizeGB

	return config.Write(m.configFile)
}

func (m *Mongod) loadUserIDs() (int, int, error) {
	uid, err := internal.GetUserID(m.config.User)
	if err != nil {
		return 0, 0, err
	}

	gid, err := internal.GetGroupID(m.config.Group)
	if err != nil {
		return 0, 0, err
	}

	return uid, gid, nil
}

func (m *Mongod) initiateFilePaths(config *mongoConfig.Config) error {
	uid, gid, err := m.loadUserIDs()
	if err != nil {
		log.Errorf("Could not load mongod uid/gid: %v", err)
		return err
	}

	log.WithFields(log.Fields{
		"tmpDir": m.config.TmpDir,
	}).Info("Initiating the mongod tmp dir")
	err = mkdir(m.config.TmpDir, uid, gid, DefaultDirMode)
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"keyFile": config.Security.KeyFile,
	}).Info("Initiating the mongod keyFile")
	err = os.Chown(config.Security.KeyFile, uid, gid)
	if err != nil {
		return err
	}
	err = os.Chmod(config.Security.KeyFile, DefaultKeyMode)
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"dbPath": config.Storage.DbPath,
	}).Info("Initiating the mongod dbPath")

	return mkdir(config.Storage.DbPath, uid, gid, DefaultDirMode)
}

func (m *Mongod) Initiate() error {
	config, err := m.loadConfig()
	if err != nil {
		log.Errorf("Could not load mongod config: %v", err)
		return err
	}

	err = m.processMongodConfig(config)
	if err != nil {
		log.Errorf("Could not process mongod config: %v", err)
		return err
	}

	return m.initiateFilePaths(config)
}

func (m *Mongod) IsStarted() bool {
	if m.command != nil {
		return m.command.IsRunning()
	}
	return false
}

func (m *Mongod) Start() error {
	err := m.Initiate()
	if err != nil {
		log.Errorf("Error initiating mongod environment on this host: %s", err)
		return err
	}

	mongodUser, err := user.Lookup(m.config.User)
	if err != nil {
		return err
	}

	mongodGroup, err := user.LookupGroup(m.config.Group)
	if err != nil {
		return err
	}

	m.Lock()
	defer m.Unlock()

	m.command, err = command.New(
		m.commandBin,
		[]string{"--config", m.configFile},
		mongodUser,
		mongodGroup,
	)
	if err != nil {
		return err
	}

	err = m.command.Start()
	if err != nil {
		return err
	}

	go m.monitorMongodCommand()
	return nil
}

func (m *Mongod) Wait() {
	m.Lock()
	defer m.Unlock()

	if m.command != nil && m.command.IsRunning() {
		m.command.Wait()
	}
}

func (m *Mongod) Kill() error {
	m.Lock()
	defer m.Unlock()

	if m.command == nil {
		return nil
	}
	return m.command.Kill()
}
