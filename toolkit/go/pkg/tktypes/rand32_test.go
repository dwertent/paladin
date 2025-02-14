// Copyright © 2024 Kaleido, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tktypes

import (
	"crypto/rand"
	"fmt"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
)

func TestRandHex(t *testing.T) {

	r1 := RandHex(32)
	assert.Len(t, r1, 64)

	randReader = iotest.ErrReader(fmt.Errorf("pop"))
	defer func() { randReader = rand.Reader }()

	assert.Panics(t, func() {
		_ = RandHex(32)
	})

}

func TestRandBytes32(t *testing.T) {
	r1 := RandBytes32()
	assert.Len(t, r1, 32)
}
