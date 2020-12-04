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

package tools

import (
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	testTmpfile     *os.File
	testFileContent = "test123456"
)

func TestMain(m *testing.M) {
	var err error
	testTmpfile, err = ioutil.TempFile("", "mongodb-orchestration-tools")
	if err != nil {
		log.Fatalf("Error setting up test tmpfile: %v", err)
	}
	_, err = testTmpfile.Write([]byte(testFileContent))
	if err != nil {
		log.Fatalf("Error writing test tmpfile: %v", err)
	}
	exit := m.Run()
	os.Remove(testTmpfile.Name())
	os.Exit(exit)
}

func TestInternalGetUserID(t *testing.T) {
	_, err := GetUserID("this-user-should-not-exist")
	assert.Error(t, err, ".GetUserID() should return error due to missing user")

	user := os.Getenv("USER")
	if user == "" {
		user = "nobody"
	}
	uid, err := GetUserID(user)
	assert.NoError(t, err, ".GetUserID() for current user should not return an error")
	assert.NotZero(t, uid, ".GetUserID() should return a uid that is not zero")
}

func TestInternalGetGroupID(t *testing.T) {
	_, err := GetGroupID("this-group-should-not-exist")
	assert.Error(t, err, ".GetGroupID() should return error due to missing group")

	currentUser, err := user.Current()
	assert.NoError(t, err, "could not get current user")
	group, err := user.LookupGroupId(currentUser.Gid)
	assert.NoError(t, err, "could not get current user group")

	gid, err := GetGroupID(group.Name)
	assert.NoError(t, err, ".GetGroupID() for current user group should not return an error")
	assert.NotEqual(t, -1, gid, ".GetGroupID() should return a gid that is not zero")
}

func TestInternalStringFromFile(t *testing.T) {
	assert.Equal(t, testFileContent, *StringFromFile(testTmpfile.Name()), ".StringFromFile() returned unexpected result")
	assert.Nil(t, StringFromFile("/does/not/exist.file"), ".StringFromFile() returned unexpected result")
}

func TestInternalPasswordFromFile(t *testing.T) {
	assert.Equal(t, testFileContent, PasswordFromFile("/", testTmpfile.Name(), "test"), ".PasswordFallbackFromFile returned unexpected result")
	assert.Equal(t, "", PasswordFromFile("/", "is-not-an-existing-file", "test"), ".PasswordFallbackFromFile returned unexpected result")
}

func TestInternalRelPathToAbs(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	assert.Equal(t, filename, RelPathToAbs(filepath.Base(filename)))
	assert.Equal(t, "", RelPathToAbs("does/not/exist"))
}
