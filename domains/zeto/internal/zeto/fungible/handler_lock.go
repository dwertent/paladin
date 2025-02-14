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

package fungible

import (
	"context"
	"encoding/json"

	"github.com/hyperledger/firefly-signer/pkg/abi"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/kaleido-io/paladin/domains/zeto/internal/msgs"
	"github.com/kaleido-io/paladin/domains/zeto/internal/zeto/common"
	corepb "github.com/kaleido-io/paladin/domains/zeto/pkg/proto"
	"github.com/kaleido-io/paladin/domains/zeto/pkg/types"
	"github.com/kaleido-io/paladin/domains/zeto/pkg/zetosigner/zetosignerapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/domain"
	"github.com/kaleido-io/paladin/toolkit/pkg/i18n"
	"github.com/kaleido-io/paladin/toolkit/pkg/plugintk"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	pb "github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"google.golang.org/protobuf/proto"
)

var _ types.DomainHandler = &lockHandler{}

type lockHandler struct {
	baseHandler
	callbacks  plugintk.DomainCallbacks
	coinSchema *pb.StateSchema
}

type TransferParams struct {
	Inputs  []interface{}             `json:"inputs"`
	Outputs []interface{}             `json:"outputs"`
	Proof   map[string]any            `json:"proof"`
	Data    ethtypes.HexBytes0xPrefix `json:"data"`
}

var lockStatesABI = &abi.Entry{
	Type: abi.Function,
	Name: "lockStates",
	Inputs: abi.ParameterArray{
		{Name: "utxos", Type: "uint256[]"},
		{Name: "proof", Type: "tuple", InternalType: "struct Commonlib.Proof", Components: common.ProofComponents},
		{Name: "delegate", Type: "address"},
		{Name: "data", Type: "bytes"},
	},
}

func NewLockHandler(name string, callbacks plugintk.DomainCallbacks, coinSchema *pb.StateSchema) *lockHandler {
	return &lockHandler{
		baseHandler: baseHandler{
			name: name,
		},
		coinSchema: coinSchema,
		callbacks:  callbacks,
	}
}

func (h *lockHandler) ValidateParams(ctx context.Context, config *types.DomainInstanceConfig, params string) (interface{}, error) {
	var lockParams types.LockParams
	if err := json.Unmarshal([]byte(params), &lockParams); err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorUnmarshalLockProofParams, err)
	}
	// the lockProof() function expects an encoded call to the transfer() function
	_, err := h.decodeTransferCall(ctx, config, lockParams.Call)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorDecodeTransferCall, err)
	}
	return &lockParams, nil
}

func (h *lockHandler) Init(ctx context.Context, tx *types.ParsedTransaction, req *prototk.InitTransactionRequest) (*prototk.InitTransactionResponse, error) {
	return &prototk.InitTransactionResponse{
		RequiredVerifiers: []*prototk.ResolveVerifierRequest{
			{
				Lookup:       tx.Transaction.From,
				Algorithm:    h.getAlgoZetoSnarkBJJ(),
				VerifierType: zetosignerapi.IDEN3_PUBKEY_BABYJUBJUB_COMPRESSED_0X,
			},
		},
	}, nil
}

func (h *lockHandler) decodeTransferCall(ctx context.Context, config *types.DomainInstanceConfig, encodedCall []byte) (*TransferParams, error) {
	transferABI := getTransferABI(config.TokenName)
	if transferABI == nil {
		return nil, i18n.NewError(ctx, msgs.MsgUnknownFunction, "transfer")
	}
	paramsJSON, err := decodeParams(ctx, transferABI, encodedCall)
	if err != nil {
		return nil, err
	}
	var params TransferParams
	err = json.Unmarshal(paramsJSON, &params)
	return &params, err
}

func (h *lockHandler) loadCoins(ctx context.Context, ids []any, stateQueryContext string) ([]*types.ZetoCoin, []*prototk.StateRef, error) {
	inputIDs := make([]any, 0, len(ids))
	stateRefs := make([]*prototk.StateRef, 0, len(ids))
	for _, input := range ids {
		parsed, err := tktypes.ParseHexUint256(ctx, input.(string))
		if err != nil {
			return nil, nil, err
		}
		if !parsed.NilOrZero() {
			inputIDs = append(inputIDs, parsed)
			stateRefs = append(stateRefs, &prototk.StateRef{
				Id:       parsed.String(),
				SchemaId: h.coinSchema.Id,
			})
		}
	}

	// TODO: this should probably query all states and not just available ones
	queryBuilder := query.NewQueryBuilder().In(".id", inputIDs)
	inputStates, err := findAvailableStates(ctx, h.callbacks, h.coinSchema, false, stateQueryContext, queryBuilder.Query().String())
	if err != nil {
		return nil, nil, err
	}
	if len(inputStates) != len(inputIDs) {
		missingStates := make([]*tktypes.HexUint256, 0, len(inputIDs))
		for _, id := range inputIDs {
			idInt := id.(*tktypes.HexUint256)
			found := false
			for _, state := range inputStates {
				if state.Id == idInt.String() {
					found = true
				}
			}
			if !found {
				missingStates = append(missingStates, idInt)
			}
		}
		return nil, nil, i18n.NewError(ctx, msgs.MsgStatesNotFound, missingStates)
	}

	inputCoins := make([]*types.ZetoCoin, len(inputStates))
	for i, state := range inputStates {
		err := json.Unmarshal([]byte(state.DataJson), &inputCoins[i])
		if err != nil {
			return nil, nil, err
		}
	}
	return inputCoins, stateRefs, nil
}

func (h *lockHandler) Assemble(ctx context.Context, tx *types.ParsedTransaction, req *prototk.AssembleTransactionRequest) (*prototk.AssembleTransactionResponse, error) {
	params := tx.Params.(*types.LockParams)
	decodedTransfer, err := h.decodeTransferCall(context.Background(), tx.DomainConfig, params.Call)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorDecodeTransferCall, err)
	}
	inputCoins, inputStates, err := h.loadCoins(ctx, decodedTransfer.Inputs, req.StateQueryContext)
	if err != nil {
		return nil, err
	}

	resolvedSender := domain.FindVerifier(tx.Transaction.From, h.getAlgoZetoSnarkBJJ(), zetosignerapi.IDEN3_PUBKEY_BABYJUBJUB_COMPRESSED_0X, req.ResolvedVerifiers)
	if resolvedSender == nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorResolveVerifier, tx.Transaction.From)
	}

	contractAddress, err := tktypes.ParseEthAddress(req.Transaction.ContractInfo.ContractAddress)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorDecodeContractAddress, err)
	}
	payloadBytes, err := h.formatProvingRequest(ctx, inputCoins, "check_utxos_owner", req.StateQueryContext, contractAddress)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorFormatProvingReq, err)
	}

	return &prototk.AssembleTransactionResponse{
		AssemblyResult: prototk.AssembleTransactionResponse_OK,
		AssembledTransaction: &prototk.AssembledTransaction{
			ReadStates: inputStates,
		},
		AttestationPlan: []*prototk.AttestationRequest{
			{
				Name:            "sender",
				AttestationType: pb.AttestationType_SIGN,
				Algorithm:       h.getAlgoZetoSnarkBJJ(),
				VerifierType:    zetosignerapi.IDEN3_PUBKEY_BABYJUBJUB_COMPRESSED_0X,
				PayloadType:     zetosignerapi.PAYLOAD_DOMAIN_ZETO_SNARK,
				Payload:         payloadBytes,
				Parties:         []string{tx.Transaction.From},
			},
		},
	}, nil
}

func (h *lockHandler) Endorse(ctx context.Context, tx *types.ParsedTransaction, req *prototk.EndorseTransactionRequest) (*prototk.EndorseTransactionResponse, error) {
	return nil, nil
}

func decodeParams(ctx context.Context, abi *abi.Entry, encodedCall []byte) ([]byte, error) {
	callData, err := abi.DecodeCallDataCtx(ctx, encodedCall)
	if err != nil {
		return nil, err
	}
	return tktypes.StandardABISerializer().SerializeJSON(callData)
}

func (h *lockHandler) Prepare(ctx context.Context, tx *types.ParsedTransaction, req *prototk.PrepareTransactionRequest) (*prototk.PrepareTransactionResponse, error) {
	params := tx.Params.(*types.LockParams)
	decodedTransfer, err := h.decodeTransferCall(context.Background(), tx.DomainConfig, params.Call)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorDecodeTransferCall, err)
	}

	var proofRes corepb.ProvingResponse
	result := domain.FindAttestation("sender", req.AttestationResult)
	if result == nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorFindSenderAttestation)
	}
	if err := proto.Unmarshal(result.Payload, &proofRes); err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorUnmarshalProvingRes, err)
	}

	data, err := common.EncodeTransactionData(ctx, req.Transaction, types.ZetoTransactionData_V0)
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorEncodeTxData, err)
	}
	LockParams := map[string]interface{}{
		"utxos":    decodedTransfer.Inputs,
		"proof":    common.EncodeProof(proofRes.Proof),
		"delegate": params.Delegate,
		"data":     data,
	}
	paramsJSON, err := json.Marshal(LockParams)
	if err != nil {
		return nil, err
	}
	functionJSON, err := json.Marshal(lockStatesABI)
	if err != nil {
		return nil, err
	}

	return &prototk.PrepareTransactionResponse{
		Transaction: &prototk.PreparedTransaction{
			FunctionAbiJson: string(functionJSON),
			ParamsJson:      string(paramsJSON),
			RequiredSigner:  &tx.Transaction.From,
		},
	}, nil
}

func (h *lockHandler) formatProvingRequest(ctx context.Context, inputCoins []*types.ZetoCoin, circuitId, stateQueryContext string, contractAddress *tktypes.EthAddress) ([]byte, error) {
	inputSize := common.GetInputSize(len(inputCoins))
	inputCommitments := make([]string, inputSize)
	inputValueInts := make([]uint64, inputSize)
	inputSalts := make([]string, inputSize)
	inputOwner := inputCoins[0].Owner.String()
	for i := 0; i < inputSize; i++ {
		if i < len(inputCoins) {
			coin := inputCoins[i]
			hash, err := coin.Hash(ctx)
			if err != nil {
				return nil, i18n.NewError(ctx, msgs.MsgErrorHashInputState, err)
			}
			inputCommitments[i] = hash.Int().Text(16)
			inputValueInts[i] = coin.Amount.Int().Uint64()
			inputSalts[i] = coin.Salt.Int().Text(16)
		} else {
			inputCommitments[i] = "0"
			inputSalts[i] = "0"
		}
	}

	tokenSecrets, err := marshalTokenSecrets(inputValueInts, []uint64{})
	if err != nil {
		return nil, i18n.NewError(ctx, msgs.MsgErrorMarshalValuesFungible, err)
	}

	payload := &corepb.ProvingRequest{
		CircuitId: circuitId,
		Common: &corepb.ProvingRequestCommon{
			InputCommitments: inputCommitments,
			InputSalts:       inputSalts,
			InputOwner:       inputOwner,
			TokenSecrets:     tokenSecrets,
			TokenType:        corepb.TokenType_fungible,
		},
	}
	return proto.Marshal(payload)
}
