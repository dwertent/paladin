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
	"encoding/json"
	"testing"

	"github.com/kaleido-io/paladin/domains/zeto/pkg/types"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
)

func sampleTransferPayload() map[string]any {
	return map[string]interface{}{
		"inputs":  []string{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
		"outputs": []string{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
		"proof": map[string]interface{}{
			"pA": []string{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
			"pB": [][]string{
				{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
				{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
			},
			"pC": []string{"0x1234567890123456789012345678901234567890", "0x1234567890123456789012345678901234567890"},
		},
		"data": "0xfeedbeef",
	}
}

func TestLockValidateParams(t *testing.T) {
	h := lockHandler{
		zeto: &Zeto{
			name: "test1",
			config: &types.DomainFactoryConfig{
				DomainContracts: types.DomainConfigContracts{
					Implementations: []*types.DomainContract{
						{
							Name:      "Zeto_Anon",
							CircuitId: "anon",
						},
					},
				},
			},
		},
	}
	config := &types.DomainInstanceConfig{
		TokenName: "test",
		CircuitId: "test",
	}
	ctx := context.Background()
	_, err := h.ValidateParams(ctx, config, "bad json")
	assert.EqualError(t, err, "PD210059: Failed to unmarshal lockProof parameters. invalid character 'b' looking for beginning of value")

	config.TokenName = "Zeto_Anon"
	lockParams := types.LockParams{
		Delegate: tktypes.RandAddress(),
		Call:     tktypes.HexBytes([]byte("bad call")),
	}
	jsonBytes, err := json.Marshal(lockParams)
	assert.NoError(t, err)
	_, err = h.ValidateParams(ctx, config, string(jsonBytes))
	assert.ErrorContains(t, err, "PD210060: Failed to decode the transfer call. FF22049: Incorrect ID for signature transfer")

	params := sampleTransferPayload()
	bytes, err := getTransferABI(config.TokenName).EncodeCallDataValues(params)
	assert.NoError(t, err)
	lockParams = types.LockParams{
		Delegate: tktypes.RandAddress(),
		Call:     tktypes.HexBytes(bytes),
	}
	jsonBytes, err = json.Marshal(lockParams)
	assert.NoError(t, err)
	res, err := h.ValidateParams(ctx, config, string(jsonBytes))
	assert.NoError(t, err)
	assert.Equal(t, lockParams, *res.(*types.LockParams))
}

func TestLocktInit(t *testing.T) {
	h := lockHandler{
		zeto: &Zeto{
			name: "test1",
		},
	}
	ctx := context.Background()
	tx := &types.ParsedTransaction{
		Params: &types.LockParams{
			Delegate: tktypes.RandAddress(),
			Call:     tktypes.HexBytes([]byte{0x01, 0x02, 0x03}),
		},
	}
	req := &prototk.InitTransactionRequest{}
	res, err := h.Init(ctx, tx, req)
	assert.NoError(t, err)
	assert.NotNil(t, res)
}

func TestLockAssemble(t *testing.T) {
	h := lockHandler{
		zeto: &Zeto{
			name: "test1",
			coinSchema: &prototk.StateSchema{
				Id: "coin",
			},
		},
	}
	ctx := context.Background()
	tx := &types.ParsedTransaction{
		Params: &types.LockParams{
			Delegate: tktypes.RandAddress(),
			Call:     tktypes.HexBytes([]byte{0x01, 0x02, 0x03}),
		},
		Transaction: &prototk.TransactionSpecification{},
	}
	req := &prototk.AssembleTransactionRequest{}
	res, err := h.Assemble(ctx, tx, req)
	assert.NoError(t, err)
	assert.NotNil(t, res)
}

func TestLockEndorse(t *testing.T) {
	h := lockHandler{
		zeto: &Zeto{
			name: "test1",
		},
	}
	ctx := context.Background()
	tx := &types.ParsedTransaction{
		Params: &types.LockParams{
			Delegate: tktypes.RandAddress(),
			Call:     tktypes.HexBytes([]byte{0x01, 0x02, 0x03}),
		},
	}
	req := &prototk.EndorseTransactionRequest{}
	res, err := h.Endorse(ctx, tx, req)
	assert.NoError(t, err)
	assert.NotNil(t, res)
}

func TestLockPrepare(t *testing.T) {
	h := lockHandler{
		zeto: &Zeto{
			name: "test1",
			config: &types.DomainFactoryConfig{
				DomainContracts: types.DomainConfigContracts{
					Implementations: []*types.DomainContract{
						{
							Name:      "Zeto_Anon",
							CircuitId: "anon",
						},
					},
				},
			},
		},
	}
	ctx := context.Background()
	tx := &types.ParsedTransaction{
		Params: &types.LockParams{
			Delegate: tktypes.RandAddress(),
			Call:     tktypes.HexBytes([]byte{0x01, 0x02, 0x03}),
		},
		DomainConfig: &types.DomainInstanceConfig{
			TokenName: "test",
		},
	}
	req := &prototk.PrepareTransactionRequest{
		Transaction: &prototk.TransactionSpecification{
			TransactionId: "bad hex",
		},
	}
	_, err := h.Prepare(ctx, tx, req)
	assert.EqualError(t, err, "PD210060: Failed to decode the transfer call. FF22048: Insufficient bytes to read signature")

	tx.DomainConfig.TokenName = "Zeto_Anon"
	transfer := getTransferABI("Zeto_Anon")
	params := sampleTransferPayload()

	bytes, err := transfer.EncodeCallDataValues(params)
	assert.NoError(t, err)
	tx.Params = &types.LockParams{
		Delegate: tktypes.RandAddress(),
		Call:     tktypes.HexBytes(bytes),
	}

	// TODO: lockProof does not currently accept a data payload
	// _, err = h.Prepare(ctx, tx, req)
	// assert.ErrorContains(t, err, "PD210049: Failed to encode transaction data. PD210028: Failed to parse transaction id. PD020007: Invalid hex:")

	req.Transaction.TransactionId = "0x1234567890123456789012345678901234567890"
	_, err = h.Prepare(ctx, tx, req)
	assert.NoError(t, err)
	assert.Equal(t, "", req.Transaction.FunctionAbiJson)
}