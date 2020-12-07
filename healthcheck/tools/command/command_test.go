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
	"testing"

	ps "github.com/mitchellh/go-ps"
	"github.com/stretchr/testify/assert"
)

func TestInternalCommandNew(t *testing.T) {
	var err error
	testCommand, err = New("echo", []string{"hello", "world"}, testCurrentUser, testCurrentGroup)
	assert.NoError(t, err, ".New() should not return an error")
	assert.Equal(t, "echo", testCommand.Bin, ".New() has incorrect Bin")
	assert.Equal(t, testCurrentUser, testCommand.User, ".New() has incorrect User")
	assert.Equal(t, testCurrentGroup, testCommand.Group, ".New() has incorrect Group")
}

func TestInternalCommandIsRunningFalse(t *testing.T) {
	assert.False(t, testCommand.IsRunning(), ".IsRunning() should be false")
}

func TestInternalCommandStart(t *testing.T) {
	assert.NoError(t, testCommand.Start(), ".Start() should not return an error")
}

func TestInternalCommandIsRunning(t *testing.T) {
	assert.True(t, testCommand.IsRunning(), ".IsRunning() should be true")
}

func TestInternalCommandWait(t *testing.T) {
	state, err := testCommand.Wait()
	assert.NoError(t, err)
	assert.True(t, state.Exited())
	assert.False(t, testCommand.IsRunning(), ".IsRunning() should be false after .Wait()")
}

func TestInternalCommandKill(t *testing.T) {
	killCommand, err := New("sleep", []string{"120"}, testCurrentUser, testCurrentGroup)
	assert.NoError(t, err, ".New() should not return an error")
	assert.NoError(t, killCommand.Start(), ".Start() should not return an error")

	killCommandProc := killCommand.command.Process
	proc, _ := ps.FindProcess(killCommandProc.Pid)
	assert.NotNil(t, proc, "cannot find started process")

	// kill the process before it's done
	assert.NoError(t, killCommand.Kill(), ".Kill() should not return an error")
	_, err = killCommandProc.Wait()
	assert.NoError(t, err)

	// check the process died
	proc, err = ps.FindProcess(killCommandProc.Pid)
	assert.Nil(t, err, "go-ps.FindProcess() should have a nil error for killed process")
	assert.Nil(t, proc, "go-ps.FindProcess() should not find the killed process")
}

func TestInternalCommandCombinedOutput(t *testing.T) {
	coCommand, err := New("echo", []string{"hello", "world"}, testCurrentUser, testCurrentGroup)
	assert.NoError(t, err, ".New() should not return an error")
	bytes, err := coCommand.CombinedOutput()
	assert.NoError(t, err, ".CombinedOutput() should not return an error")
	assert.NotEmpty(t, bytes, ".CombinedOutput() should not return empty bytes")
	assert.Equal(t, "hello world\n", string(bytes), ".CombinedOutput() has unexpected output")
}
