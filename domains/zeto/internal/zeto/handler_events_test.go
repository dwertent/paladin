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

package zeto

import (
	"context"
	"testing"

	"github.com/kaleido-io/paladin/domains/zeto/internal/zeto/smt"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleMintEvent(t *testing.T) {
	z, testCallbacks := newTestZeto()
	storage := smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err := smt.NewSmt(storage)
	require.NoError(t, err)
	ctx := context.Background()

	ev := &prototk.OnChainEvent{
		DataJson:          "bad json",
		SoliditySignature: "event UTXOMint(uint256[] outputs, address indexed submitter, bytes data)",
	}
	res := &prototk.HandleEventBatchResponse{}

	// bad transaction data for the mint event - should be logged and move on
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "testToken1", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "testToken1", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001ffff\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "testToken1", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "testToken1", res)
	assert.NoError(t, err)
	assert.Equal(t, "0x30e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000", res.TransactionsComplete[0].TransactionId)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.ErrorContains(t, err, "PD210061: Failed to update merkle tree for the UTXOMint event. PD210056: Failed to create new node index from hash. 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	storage = smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err = smt.NewSmt(storage)
	require.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleMintEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	assert.Len(t, res.TransactionsComplete, 3)
	newStates, err := storage.GetNewStates()
	require.NoError(t, err)
	assert.Len(t, newStates, 2)
	assert.Equal(t, "merkle_tree_root", newStates[0].SchemaId)
	assert.Equal(t, "merkle_tree_node", newStates[1].SchemaId)

}

func TestHandleTransferEvent(t *testing.T) {
	z, testCallbacks := newTestZeto()
	storage := smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err := smt.NewSmt(storage)
	require.NoError(t, err)
	ctx := context.Background()

	ev := &prototk.OnChainEvent{
		DataJson:          "bad json",
		SoliditySignature: "event UTXOTransfer(uint256[] inputs, uint256[] outputs, address indexed submitter, bytes data)",
	}
	res := &prototk.HandleEventBatchResponse{}

	// bad data for the transfer event - should be logged and move on
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001ffff\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	assert.Equal(t, "0x30e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000", res.TransactionsComplete[0].TransactionId)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.ErrorContains(t, err, "PD210061: Failed to update merkle tree for the UTXOTransfer event. PD210056: Failed to create new node index from hash. 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	storage = smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err = smt.NewSmt(storage)
	require.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	assert.Len(t, res.TransactionsComplete, 3)
	// assert.Len(t, res6.NewStates, 2)
	// assert.Equal(t, "merkle_tree_root", res6.NewStates[0].SchemaId)
	// assert.Equal(t, "merkle_tree_node", res6.NewStates[1].SchemaId)
}

func TestHandleTransferWithEncryptionEvent(t *testing.T) {
	z, testCallbacks := newTestZeto()
	storage := smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err := smt.NewSmt(storage)
	require.NoError(t, err)
	ctx := context.Background()

	ev := &prototk.OnChainEvent{
		DataJson:          "bad json",
		SoliditySignature: "event UTXOTransferWithEncryptedValues(uint256[] inputs, uint256[] outputs, uint256 encryptionNonce, uint256[2] ecdhPublicKey, uint256[] encryptedValues, address indexed submitter, bytes data)",
	}
	res := &prototk.HandleEventBatchResponse{}

	// bad data for the transfer event - should be logged and move on
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001ffff\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	assert.Equal(t, "0x30e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000", res.TransactionsComplete[0].TransactionId)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.ErrorContains(t, err, "PD210061: Failed to update merkle tree for the UTXOTransferWithEncryptedValues event. PD210056: Failed to create new node index from hash. 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	storage = smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err = smt.NewSmt(storage)
	require.NoError(t, err)
	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"outputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleTransferWithEncryptionEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.NoError(t, err)
	assert.Equal(t, "0x30e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000", res.TransactionsComplete[0].TransactionId)
}

func TestUpdateMerkleTree(t *testing.T) {
	z, testCallbacks := newTestZeto()
	storage := smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err := smt.NewSmt(storage)
	require.NoError(t, err)

	ctx := context.Background()
	err = z.updateMerkleTree(ctx, merkleTree, storage, tktypes.HexBytes("0x1234"), []tktypes.HexUint256{*tktypes.MustParseHexUint256("0x1234"), *tktypes.MustParseHexUint256("0x0")})
	assert.NoError(t, err)
}

func TestHandleWithdrawEvent(t *testing.T) {
	z, testCallbacks := newTestZeto()
	storage := smt.NewStatesStorage(testCallbacks, "testToken1", "context1", "merkle_tree_root", "merkle_tree_node")
	merkleTree, err := smt.NewSmt(storage)
	require.NoError(t, err)
	ctx := context.Background()

	ev := &prototk.OnChainEvent{
		DataJson:          "bad json",
		SoliditySignature: "event UTXOWithdraw(uint256 amount, uint256[] inputs, uint256 output, address indexed submitter, bytes data)",
	}
	res := &prototk.HandleEventBatchResponse{}

	// bad data for the withdraw event - should be logged and move on
	err = z.handleWithdrawEvent(ctx, merkleTree, storage, ev, "Zeto_Anon", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001\",\"inputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"output\":\"7980718117603030807695495350922077879582656644717071592146865497574198464253\",\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleWithdrawEvent(ctx, merkleTree, storage, ev, "Zeto_Anon", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001ffff\",\"inputs\":[\"7980718117603030807695495350922077879582656644717071592146865497574198464253\"],\"output\":\"7980718117603030807695495350922077879582656644717071592146865497574198464253\",\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleWithdrawEvent(ctx, merkleTree, storage, ev, "Zeto_Anon", res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\",\"output\":\"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\",\"submitter\":\"0x74e71b05854ee819cb9397be01c82570a178d019\"}"
	err = z.handleWithdrawEvent(ctx, merkleTree, storage, ev, "Zeto_AnonNullifier", res)
	assert.ErrorContains(t, err, "PD210061: Failed to update merkle tree for the UTXOWithdraw event. PD210056: Failed to create new node index from hash. 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
}

func TestParseStatesFromEvent(t *testing.T) {
	txID := tktypes.MustParseHexBytes("0x1234")
	states := parseStatesFromEvent(txID, []tktypes.HexUint256{*tktypes.MustParseHexUint256("0x1234"), *tktypes.MustParseHexUint256("0x0")})
	assert.Len(t, states, 2)
	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000001234", states[0].Id)
	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000000", states[1].Id)
}
func TestHandleLockedEvent(t *testing.T) {
	z, _ := newTestZeto()
	ctx := context.Background()

	ev := &prototk.OnChainEvent{
		DataJson:          "bad json",
		SoliditySignature: "event Locked(bytes data)",
	}
	res := &prototk.HandleEventBatchResponse{}

	// bad data for the locked event - should be logged and move on
	err := z.handleLockedEvent(ctx, ev, res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001\"}"
	err = z.handleLockedEvent(ctx, ev, res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001ffff\"}"
	err = z.handleLockedEvent(ctx, ev, res)
	assert.NoError(t, err)

	ev.DataJson = "{\"data\":\"0x0001000030e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000\"}"
	err = z.handleLockedEvent(ctx, ev, res)
	assert.NoError(t, err)
	assert.Equal(t, "0x30e43028afbb41d6887444f4c2b4ed6d00000000000000000000000000000000", res.TransactionsComplete[0].TransactionId)
}
