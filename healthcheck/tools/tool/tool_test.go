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

package tool

import (
	"testing"

	tools "github.com/percona/percona-server-mongodb-operator/healthcheck"
	"github.com/stretchr/testify/assert"
)

func TestInternalToolNew(t *testing.T) {
	testApp, _ := New("test help", "git-commit-here", "branch-name-here")
	appModel := testApp.Model()
	assert.Contains(t, appModel.Version, "tool.test version "+tools.Version+"\ngit commit git-commit-here, branch branch-name-here\ngo version", "kingpin.Application version is unexpected")
	assert.Equal(t, Author, appModel.Author, "kingpin.Application author is unexpected")
	assert.Equal(t, "test help", appModel.Help, "kingpin.Application help is unexpected")
}
