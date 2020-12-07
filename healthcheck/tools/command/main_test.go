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
	"os"
	"os/user"
	"testing"
)

var testCommand *Command
var testCurrentUser *user.User
var testCurrentGroup *user.Group

func TestMain(m *testing.M) {
	var err error

	testCurrentUser, err = user.Current()
	if err != nil {
		panic(err)
	}

	testCurrentGroup, err = user.LookupGroupId(testCurrentUser.Gid)
	if err != nil {
		panic(err)
	}

	defer func() {
		if testCommand != nil {
			testCommand.Kill()
		}
	}()
	os.Exit(m.Run())
}
