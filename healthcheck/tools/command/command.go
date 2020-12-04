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

package command

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	log "github.com/sirupsen/logrus"
)

type Command struct {
	sync.Mutex
	Bin   string
	Args  []string
	User  *user.User
	Group *user.Group

	command *exec.Cmd
	running bool
}

func New(bin string, args []string, user *user.User, group *user.Group) (*Command, error) {
	c := &Command{
		Bin:   bin,
		Args:  args,
		User:  user,
		Group: group,
	}
	return c, c.prepare()
}

func (c *Command) IsRunning() bool {
	c.Lock()
	defer c.Unlock()
	return c.running
}

func (c *Command) doChangeUser() bool {
	currentUser, err := user.Current()
	if err != nil {
		return true
	}
	if c.User.Name == currentUser.Name && currentUser.Gid == c.Group.Gid {
		return false
	}
	return true
}

func (c *Command) prepare() error {
	_, err := exec.LookPath(c.Bin)
	if err != nil {
		if _, err := os.Stat(c.Bin); os.IsNotExist(err) {
			return err
		}
	}
	c.command = exec.Command(c.Bin, c.Args...)
	if c.doChangeUser() {
		uid, err := strconv.Atoi(c.User.Uid)
		if err != nil {
			return err
		}

		gid, err := strconv.Atoi(c.User.Gid)
		if err != nil {
			return err
		}

		c.command.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(gid),
			},
		}
	}
	return nil
}

func (c *Command) Start() error {
	c.Lock()
	defer c.Unlock()

	log.WithFields(log.Fields{
		"command": c.Bin,
		"args":    c.Args,
		"user":    c.User.Name,
		"group":   c.Group.Name,
	}).Debug("Starting command")

	c.command.Stdout = os.Stdout
	c.command.Stderr = os.Stderr

	err := c.command.Start()
	if err != nil {
		return err
	}
	c.running = true

	return nil
}

func (c *Command) CombinedOutput() ([]byte, error) {
	log.WithFields(log.Fields{
		"command": c.Bin,
		"args":    c.Args,
		"user":    c.User.Name,
		"group":   c.Group.Name,
	}).Debug("Running command")

	return c.command.CombinedOutput()
}

func (c *Command) Run() error {
	log.WithFields(log.Fields{
		"command": c.Bin,
		"args":    c.Args,
		"user":    c.User.Name,
		"group":   c.Group.Name,
	}).Debug("Running command")

	return c.command.Run()
}

func (c *Command) Wait() (*os.ProcessState, error) {
	if c.IsRunning() {
		state, err := c.command.Process.Wait()
		if err != nil {
			return nil, err
		}

		c.Lock()
		defer c.Unlock()
		c.running = !state.Exited()
		return state, nil
	}
	return nil, errors.New("not running")
}

func (c *Command) Kill() error {
	if c.command.Process == nil {
		return nil
	}

	c.Lock()
	defer c.Unlock()

	err := c.command.Process.Kill()
	if err != nil {
		return err
	}
	c.running = false

	return err
}
