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

package noto

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hyperledger/firefly-signer/pkg/abi"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/hyperledger/firefly-signer/pkg/secp256k1"
	"github.com/kaleido-io/paladin/domains/noto/pkg/types"
	"github.com/kaleido-io/paladin/sdk/go/pkg/pldtypes"
	"github.com/kaleido-io/paladin/toolkit/pkg/algorithms"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/verifiers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var pTrue = true
var notoBasicConfig = &types.NotoParsedConfig{
	NotaryMode:   types.NotaryModeBasic.Enum(),
	NotaryLookup: "notary@node1",
	Options: types.NotoOptions{
		Basic: &types.NotoBasicOptions{
			RestrictMint: &pTrue,
			AllowBurn:    &pTrue,
			AllowLock:    &pTrue,
		},
	},
}

func TestTransfer(t *testing.T) {
	n := &Noto{
		Callbacks:  mockCallbacks,
		coinSchema: &prototk.StateSchema{Id: "coin"},
		dataSchema: &prototk.StateSchema{Id: "data"},
	}
	ctx := context.Background()
	fn := types.NotoABI.Functions()["transfer"]

	notaryAddress := "0x1000000000000000000000000000000000000000"
	receiverAddress := "0x2000000000000000000000000000000000000000"
	senderKey, err := secp256k1.GenerateSecp256k1KeyPair()
	require.NoError(t, err)

	inputCoin := &types.NotoCoinState{
		ID: pldtypes.RandBytes32(),
		Data: types.NotoCoin{
			Owner:  (*pldtypes.EthAddress)(&senderKey.Address),
			Amount: pldtypes.Int64ToInt256(100),
		},
	}
	mockCallbacks.MockFindAvailableStates = func() (*prototk.FindAvailableStatesResponse, error) {
		return &prototk.FindAvailableStatesResponse{
			States: []*prototk.StoredState{
				{
					Id:       inputCoin.ID.String(),
					SchemaId: "coin",
					DataJson: mustParseJSON(inputCoin.Data),
				},
			},
		}, nil
	}

	contractAddress := "0xf6a75f065db3cef95de7aa786eee1d0cb1aeafc3"
	tx := &prototk.TransactionSpecification{
		TransactionId: "0x015e1881f2ba769c22d05c841f06949ec6e1bd573f5e1e0328885494212f077d",
		From:          "sender@node1",
		ContractInfo: &prototk.ContractInfo{
			ContractAddress:    contractAddress,
			ContractConfigJson: mustParseJSON(notoBasicConfig),
		},
		FunctionAbiJson:   mustParseJSON(fn),
		FunctionSignature: fn.SolString(),
		FunctionParamsJson: `{
			"to": "receiver@node2",
			"amount": 75,
			"data": "0x1234"
		}`,
	}

	initRes, err := n.InitTransaction(ctx, &prototk.InitTransactionRequest{
		Transaction: tx,
	})
	require.NoError(t, err)
	require.Len(t, initRes.RequiredVerifiers, 3)
	assert.Equal(t, "notary@node1", initRes.RequiredVerifiers[0].Lookup)
	assert.Equal(t, "sender@node1", initRes.RequiredVerifiers[1].Lookup)
	assert.Equal(t, "receiver@node2", initRes.RequiredVerifiers[2].Lookup)

	verifiers := []*prototk.ResolvedVerifier{
		{
			Lookup:       "notary@node1",
			Algorithm:    algorithms.ECDSA_SECP256K1,
			VerifierType: verifiers.ETH_ADDRESS,
			Verifier:     notaryAddress,
		},
		{
			Lookup:       "sender@node1",
			Algorithm:    algorithms.ECDSA_SECP256K1,
			VerifierType: verifiers.ETH_ADDRESS,
			Verifier:     senderKey.Address.String(),
		},
		{
			Lookup:       "receiver@node2",
			Algorithm:    algorithms.ECDSA_SECP256K1,
			VerifierType: verifiers.ETH_ADDRESS,
			Verifier:     receiverAddress,
		},
	}

	assembleRes, err := n.AssembleTransaction(ctx, &prototk.AssembleTransactionRequest{
		Transaction:       tx,
		ResolvedVerifiers: verifiers,
	})
	require.NoError(t, err)
	assert.Equal(t, prototk.AssembleTransactionResponse_OK, assembleRes.AssemblyResult)
	require.Len(t, assembleRes.AssembledTransaction.InputStates, 1)
	require.Len(t, assembleRes.AssembledTransaction.OutputStates, 2)
	require.Len(t, assembleRes.AssembledTransaction.ReadStates, 0)
	require.Len(t, assembleRes.AssembledTransaction.InfoStates, 1)
	assert.Equal(t, inputCoin.ID.String(), assembleRes.AssembledTransaction.InputStates[0].Id)

	outputCoin, err := n.unmarshalCoin(assembleRes.AssembledTransaction.OutputStates[0].StateDataJson)
	require.NoError(t, err)
	assert.Equal(t, receiverAddress, outputCoin.Owner.String())
	assert.Equal(t, "75", outputCoin.Amount.Int().String())
	assert.Equal(t, []string{"notary@node1", "sender@node1", "receiver@node2"}, assembleRes.AssembledTransaction.OutputStates[0].DistributionList)

	remainderCoin, err := n.unmarshalCoin(assembleRes.AssembledTransaction.OutputStates[1].StateDataJson)
	require.NoError(t, err)
	assert.Equal(t, senderKey.Address.String(), remainderCoin.Owner.String())
	assert.Equal(t, "25", remainderCoin.Amount.Int().String())
	assert.Equal(t, []string{"notary@node1", "sender@node1"}, assembleRes.AssembledTransaction.OutputStates[1].DistributionList)

	outputInfo, err := n.unmarshalInfo(assembleRes.AssembledTransaction.InfoStates[0].StateDataJson)
	require.NoError(t, err)
	assert.Equal(t, "0x1234", outputInfo.Data.String())
	assert.Equal(t, []string{"notary@node1", "sender@node1", "receiver@node2"}, assembleRes.AssembledTransaction.InfoStates[0].DistributionList)

	encodedTransfer, err := n.encodeTransferUnmasked(ctx, ethtypes.MustNewAddress(contractAddress),
		[]*types.NotoCoin{&inputCoin.Data},
		[]*types.NotoCoin{outputCoin, remainderCoin},
	)
	require.NoError(t, err)
	signature, err := senderKey.SignDirect(encodedTransfer)
	require.NoError(t, err)
	signatureBytes := pldtypes.HexBytes(signature.CompactRSV())

	inputStates := []*prototk.EndorsableState{
		{
			SchemaId:      "coin",
			Id:            inputCoin.ID.String(),
			StateDataJson: mustParseJSON(inputCoin.Data),
		},
	}
	outputStates := []*prototk.EndorsableState{
		{
			SchemaId:      "coin",
			Id:            "0x0000000000000000000000000000000000000000000000000000000000000001",
			StateDataJson: assembleRes.AssembledTransaction.OutputStates[0].StateDataJson,
		},
		{
			SchemaId:      "coin",
			Id:            "0x0000000000000000000000000000000000000000000000000000000000000002",
			StateDataJson: assembleRes.AssembledTransaction.OutputStates[1].StateDataJson,
		},
	}
	infoStates := []*prototk.EndorsableState{
		{
			SchemaId:      "data",
			Id:            "0x0000000000000000000000000000000000000000000000000000000000000003",
			StateDataJson: assembleRes.AssembledTransaction.InfoStates[0].StateDataJson,
		},
	}

	endorseRes, err := n.EndorseTransaction(ctx, &prototk.EndorseTransactionRequest{
		Transaction:       tx,
		ResolvedVerifiers: verifiers,
		Inputs:            inputStates,
		Outputs:           outputStates,
		Info:              infoStates,
		EndorsementRequest: &prototk.AttestationRequest{
			Name: "notary",
		},
		Signatures: []*prototk.AttestationResult{
			{
				Name:     "sender",
				Verifier: &prototk.ResolvedVerifier{Verifier: senderKey.Address.String()},
				Payload:  signatureBytes,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, prototk.EndorseTransactionResponse_ENDORSER_SUBMIT, endorseRes.EndorsementResult)

	// Prepare once to test base invoke
	prepareRes, err := n.PrepareTransaction(ctx, &prototk.PrepareTransactionRequest{
		Transaction:       tx,
		ResolvedVerifiers: verifiers,
		InputStates:       inputStates,
		OutputStates:      outputStates,
		InfoStates:        infoStates,
		AttestationResult: []*prototk.AttestationResult{
			{
				Name:     "sender",
				Verifier: &prototk.ResolvedVerifier{Verifier: senderKey.Address.String()},
				Payload:  signatureBytes,
			},
			{
				Name:     "notary",
				Verifier: &prototk.ResolvedVerifier{Lookup: "notary@node1"},
			},
		},
	})
	require.NoError(t, err)
	expectedFunction := mustParseJSON(interfaceBuild.ABI.Functions()["transfer"])
	assert.JSONEq(t, expectedFunction, prepareRes.Transaction.FunctionAbiJson)
	assert.Nil(t, prepareRes.Transaction.ContractAddress)
	assert.JSONEq(t, fmt.Sprintf(`{
		"inputs": ["%s"],
		"outputs": ["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"],
		"signature": "%s",
		"txId": "0x015e1881f2ba769c22d05c841f06949ec6e1bd573f5e1e0328885494212f077d",
		"data": "0x00010000015e1881f2ba769c22d05c841f06949ec6e1bd573f5e1e0328885494212f077d000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000003"
	}`, inputCoin.ID, signatureBytes), prepareRes.Transaction.ParamsJson)

	var invokeFn abi.Entry
	err = json.Unmarshal([]byte(prepareRes.Transaction.FunctionAbiJson), &invokeFn)
	require.NoError(t, err)
	encodedCall, err := invokeFn.EncodeCallDataJSONCtx(ctx, []byte(prepareRes.Transaction.ParamsJson))
	require.NoError(t, err)

	// Prepare again to test hook invoke
	hookAddress := "0x515fba7fe1d8b9181be074bd4c7119544426837c"
	tx.ContractInfo.ContractConfigJson = mustParseJSON(&types.NotoParsedConfig{
		NotaryLookup: "notary@node1",
		NotaryMode:   types.NotaryModeHooks.Enum(),
		Options: types.NotoOptions{
			Hooks: &types.NotoHooksOptions{
				PublicAddress:     pldtypes.MustEthAddress(hookAddress),
				DevUsePublicHooks: true,
			},
		},
	})
	prepareRes, err = n.PrepareTransaction(ctx, &prototk.PrepareTransactionRequest{
		Transaction:       tx,
		ResolvedVerifiers: verifiers,
		InputStates:       inputStates,
		OutputStates:      outputStates,
		InfoStates:        infoStates,
		AttestationResult: []*prototk.AttestationResult{
			{
				Name:     "sender",
				Verifier: &prototk.ResolvedVerifier{Verifier: senderKey.Address.String()},
				Payload:  signatureBytes,
			},
			{
				Name:     "notary",
				Verifier: &prototk.ResolvedVerifier{Lookup: "notary@node1"},
			},
		},
	})
	require.NoError(t, err)
	expectedFunction = mustParseJSON(hooksBuild.ABI.Functions()["onTransfer"])
	assert.JSONEq(t, expectedFunction, prepareRes.Transaction.FunctionAbiJson)
	assert.Equal(t, &hookAddress, prepareRes.Transaction.ContractAddress)
	assert.JSONEq(t, fmt.Sprintf(`{
		"sender": "%s",
		"from": "%s",
		"to": "0x2000000000000000000000000000000000000000",
		"amount": "0x4b",
		"data": "0x1234",
		"prepared": {
			"contractAddress": "%s",
			"encodedCall": "%s"
		}
	}`, senderKey.Address, senderKey.Address, contractAddress, pldtypes.HexBytes(encodedCall)), prepareRes.Transaction.ParamsJson)
}

func TestTransferAssembleMissingFrom(t *testing.T) {
	n := &Noto{
		Callbacks:  mockCallbacks,
		coinSchema: &prototk.StateSchema{Id: "coin"},
		dataSchema: &prototk.StateSchema{Id: "data"},
	}
	h := transferHandler{noto: n}
	ctx := context.Background()

	fn := types.NotoABI.Functions()["transfer"]
	contractAddress := "0xf6a75f065db3cef95de7aa786eee1d0cb1aeafc3"
	parsedTx := &types.ParsedTransaction{
		Transaction: &prototk.TransactionSpecification{
			From: "sender@node1",
		},
		FunctionABI:     fn,
		ContractAddress: ethtypes.MustNewAddress(contractAddress),
		DomainConfig:    notoBasicConfig,
		Params: &types.TransferParams{
			To:     "receiver@node2",
			Amount: pldtypes.Int64ToInt256(75),
			Data:   pldtypes.MustParseHexBytes("0x1234"),
		},
	}
	req := &prototk.AssembleTransactionRequest{
		ResolvedVerifiers: []*prototk.ResolvedVerifier{},
	}

	_, err := h.Assemble(ctx, parsedTx, req)
	assert.Regexp(t, "PD200011.*'from'", err)
}

func TestTransferAssembleMissingTo(t *testing.T) {
	n := &Noto{
		Callbacks:  mockCallbacks,
		coinSchema: &prototk.StateSchema{Id: "coin"},
		dataSchema: &prototk.StateSchema{Id: "data"},
	}
	h := transferHandler{noto: n}
	ctx := context.Background()

	fn := types.NotoABI.Functions()["transfer"]
	contractAddress := "0xf6a75f065db3cef95de7aa786eee1d0cb1aeafc3"
	parsedTx := &types.ParsedTransaction{
		Transaction: &prototk.TransactionSpecification{
			From: "sender@node1",
		},
		FunctionABI:     fn,
		ContractAddress: ethtypes.MustNewAddress(contractAddress),
		DomainConfig:    notoBasicConfig,
		Params: &types.TransferParams{
			To:     "receiver@node2",
			Amount: pldtypes.Int64ToInt256(75),
			Data:   pldtypes.MustParseHexBytes("0x1234"),
		},
	}
	req := &prototk.AssembleTransactionRequest{
		ResolvedVerifiers: []*prototk.ResolvedVerifier{
			{
				Lookup:       "sender@node1",
				Algorithm:    algorithms.ECDSA_SECP256K1,
				VerifierType: verifiers.ETH_ADDRESS,
				Verifier:     "0x1000000000000000000000000000000000000000",
			},
		},
	}

	_, err := h.Assemble(ctx, parsedTx, req)
	assert.Regexp(t, "PD200011.*'to'", err)
}
