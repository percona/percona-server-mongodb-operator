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

package api

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	testAPI       = &SDKClient{}
	testAPIConfig = &Config{
		Host:    DefaultSchedulerHost,
		Timeout: time.Second,
		Secure:  true,
	}
)

func TestMain(m *testing.M) {
	testAPI = New(testAPIConfig)
	os.Exit(m.Run())
}

func TestInternalAPINew(t *testing.T) {
	testAPI = nil
	assert.Nil(t, testAPI)

	testAPI = New(testAPIConfig)
	assert.Equal(t, testAPIConfig, testAPI.config, "api.config is incorrect")
	assert.Equal(t, HTTPSchemeSecure, testAPI.scheme, "api.scheme is incorrect")

	testAPIConfig.Secure = false
	testAPI = New(testAPIConfig)
	assert.Equal(t, HTTPSchemePlain, testAPI.scheme, "api.scheme is incorrect")
}
