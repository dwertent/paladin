/*
 * Copyright © 2024 Kaleido, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
 * the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
 * an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package pldclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPTXModule(t *testing.T) {
	testRPCModule(t, func(c PaladinClient) RPCModule { return c.PTX() })
}

func TestSubscribe(t *testing.T) {
	ctx, c, _, done := newTestClientAndServerWebSockets(t)
	defer done()

	_, err := c.PTX().SubscribeReceipts(ctx, "listener1")
	require.Regexp(t, "PD020702", err)
}

func TestSubscribeNotWS(t *testing.T) {
	ctx, c, _, done := newTestClientAndServerHTTP(t)
	defer done()

	_, err := c.PTX().SubscribeReceipts(ctx, "listener1")
	require.Regexp(t, "PD020217", err)
}
