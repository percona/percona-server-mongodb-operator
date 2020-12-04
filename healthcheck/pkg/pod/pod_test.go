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

package pod

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPkgPodPods(t *testing.T) {
	p := NewPods()
	assert.Len(t, p.Get(), 0)
	p.Set([]string{"test"})

	pods := p.Get()
	assert.Len(t, pods, 1)
	assert.Equal(t, "test", pods[0])

	assert.True(t, p.Has("test"))
	p.Set([]string{"false"})
	assert.False(t, p.Has("test"))
}
