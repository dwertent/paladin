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
	"math/big"

	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/kaleido-io/paladin/domains/noto/internal/msgs"
	"github.com/kaleido-io/paladin/domains/noto/pkg/types"
	"github.com/kaleido-io/paladin/toolkit/pkg/algorithms"
	"github.com/kaleido-io/paladin/toolkit/pkg/domain"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/signpayloads"
	"github.com/kaleido-io/paladin/toolkit/pkg/solutils"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/kaleido-io/paladin/toolkit/pkg/verifiers"
)

type burnHandler struct {
	noto *Noto
}

func (h *burnHandler) ValidateParams(ctx context.Context, config *types.NotoParsedConfig, params string) (interface{}, error) {
	var BurnParams types.BurnParams
	if err := json.Unmarshal([]byte(params), &BurnParams); err != nil {
		return nil, err
	}
	if BurnParams.Amount == nil || BurnParams.Amount.Int().Sign() != 1 {
		return nil, i18n.NewError(ctx, msgs.MsgParameterGreaterThanZero, "amount")
	}
	return &BurnParams, nil
}

func (h *burnHandler) Init(ctx context.Context, tx *types.ParsedTransaction, req *prototk.InitTransactionRequest) (*prototk.InitTransactionResponse, error) {
	notary := tx.DomainConfig.NotaryLookup

	if !tx.DomainConfig.AllowBurning {
		return nil, i18n.NewError(ctx, msgs.MsgNoBurning)
	}
	return &prototk.InitTransactionResponse{
		RequiredVerifiers: []*prototk.ResolveVerifierRequest{
			{
				Lookup:       notary,
				Algorithm:    algorithms.ECDSA_SECP256K1,
				VerifierType: verifiers.ETH_ADDRESS,
			},
			{
				Lookup:       tx.Transaction.From,
				Algorithm:    algorithms.ECDSA_SECP256K1,
				VerifierType: verifiers.ETH_ADDRESS,
			},
		},
	}, nil
}

func (h *burnHandler) Assemble(ctx context.Context, tx *types.ParsedTransaction, req *prototk.AssembleTransactionRequest) (*prototk.AssembleTransactionResponse, error) {
	params := tx.Params.(*types.BurnParams)
	notary := tx.DomainConfig.NotaryLookup

	_, err := h.noto.findEthAddressVerifier(ctx, "notary", notary, req.ResolvedVerifiers)
	if err != nil {
		return nil, err
	}
	fromAddress, err := h.noto.findEthAddressVerifier(ctx, "from", tx.Transaction.From, req.ResolvedVerifiers)
	if err != nil {
		return nil, err
	}

	inputCoins, inputStates, total, err := h.noto.prepareInputs(ctx, req.StateQueryContext, fromAddress, params.Amount)
	if err != nil {
		return nil, err
	}
	infoStates, err := h.noto.prepareInfo(params.Data, []string{notary, tx.Transaction.From})
	if err != nil {
		return nil, err
	}

	var outputCoins []*types.NotoCoin
	var outputStates []*prototk.NewState
	if total.Cmp(params.Amount.Int()) == 1 {
		remainder := big.NewInt(0).Sub(total, params.Amount.Int())
		returnedCoins, returnedStates, err := h.noto.prepareOutputs(fromAddress, (*tktypes.HexUint256)(remainder), []string{notary, tx.Transaction.From})
		if err != nil {
			return nil, err
		}
		outputCoins = append(outputCoins, returnedCoins...)
		outputStates = append(outputStates, returnedStates...)
	}

	encodedTransfer, err := h.noto.encodeTransferUnmasked(ctx, tx.ContractAddress, inputCoins, outputCoins)
	if err != nil {
		return nil, err
	}

	return &prototk.AssembleTransactionResponse{
		AssemblyResult: prototk.AssembleTransactionResponse_OK,
		AssembledTransaction: &prototk.AssembledTransaction{
			InputStates:  inputStates,
			OutputStates: outputStates,
			InfoStates:   infoStates,
		},
		AttestationPlan: []*prototk.AttestationRequest{
			// Sender confirms the initial request with a signature
			{
				Name:            "sender",
				AttestationType: prototk.AttestationType_SIGN,
				Algorithm:       algorithms.ECDSA_SECP256K1,
				VerifierType:    verifiers.ETH_ADDRESS,
				Payload:         encodedTransfer,
				PayloadType:     signpayloads.OPAQUE_TO_RSV,
				Parties:         []string{req.Transaction.From},
			},
			// Notary will endorse the assembled transaction (by submitting to the ledger)
			{
				Name:            "notary",
				AttestationType: prototk.AttestationType_ENDORSE,
				Algorithm:       algorithms.ECDSA_SECP256K1,
				VerifierType:    verifiers.ETH_ADDRESS,
				Parties:         []string{notary},
			},
		},
	}, nil
}

func (h *burnHandler) Endorse(ctx context.Context, tx *types.ParsedTransaction, req *prototk.EndorseTransactionRequest) (*prototk.EndorseTransactionResponse, error) {
	params := tx.Params.(*types.BurnParams)
	coins, err := h.noto.gatherCoins(ctx, req.Inputs, req.Outputs)
	if err != nil {
		return nil, err
	}
	if err := h.noto.validateBurnAmounts(ctx, params, coins); err != nil {
		return nil, err
	}
	if err := h.noto.validateOwners(ctx, tx, req, coins); err != nil {
		return nil, err
	}

	// Notary checks the signature from the sender, then submits the transaction
	if err := h.noto.validateTransferSignature(ctx, tx, req, coins); err != nil {
		return nil, err
	}
	return &prototk.EndorseTransactionResponse{
		EndorsementResult: prototk.EndorseTransactionResponse_ENDORSER_SUBMIT,
	}, nil
}

func (h *burnHandler) baseLedgerBurn(ctx context.Context, req *prototk.PrepareTransactionRequest) (*TransactionWrapper, error) {
	inputs := make([]string, len(req.InputStates))
	for i, state := range req.InputStates {
		inputs[i] = state.Id
	}
	outputs := make([]string, len(req.OutputStates))
	for i, state := range req.OutputStates {
		outputs[i] = state.Id
	}

	// Include the signature from the sender/notary
	// This is not verified on the base ledger, but can be verified by anyone with the unmasked state data
	sender := domain.FindAttestation("sender", req.AttestationResult)
	if sender == nil {
		return nil, i18n.NewError(ctx, msgs.MsgAttestationNotFound, "sender")
	}

	data, err := h.noto.encodeTransactionData(ctx, req.Transaction, req.InfoStates)
	if err != nil {
		return nil, err
	}
	params := &NotoTransferParams{
		Inputs:    inputs,
		Outputs:   outputs,
		Signature: sender.Payload,
		Data:      data,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &TransactionWrapper{
		transactionType: prototk.PreparedTransaction_PUBLIC,
		functionABI:     h.noto.contractABI.Functions()["transfer"],
		paramsJSON:      paramsJSON,
	}, nil
}

func (h *burnHandler) hookBurn(ctx context.Context, tx *types.ParsedTransaction, req *prototk.PrepareTransactionRequest, baseTransaction *TransactionWrapper) (*TransactionWrapper, error) {
	inParams := tx.Params.(*types.BurnParams)

	fromAddress, err := h.noto.findEthAddressVerifier(ctx, "from", tx.Transaction.From, req.ResolvedVerifiers)
	if err != nil {
		return nil, err
	}

	encodedCall, err := baseTransaction.encode(ctx)
	if err != nil {
		return nil, err
	}
	params := &BurnHookParams{
		Sender: fromAddress,
		From:   fromAddress,
		Amount: inParams.Amount,
		Prepared: PreparedTransaction{
			ContractAddress: (*tktypes.EthAddress)(tx.ContractAddress),
			EncodedCall:     encodedCall,
		},
	}

	transactionType := prototk.PreparedTransaction_PUBLIC
	functionABI := solutils.MustLoadBuild(notoHooksJSON).ABI.Functions()["onBurn"]
	var paramsJSON []byte

	if tx.DomainConfig.PrivateAddress != nil {
		transactionType = prototk.PreparedTransaction_PRIVATE
		functionABI = penteInvokeABI("onBurn", functionABI.Inputs)
		penteParams := &PenteInvokeParams{
			Group:  tx.DomainConfig.PrivateGroup,
			To:     tx.DomainConfig.PrivateAddress,
			Inputs: params,
		}
		paramsJSON, err = json.Marshal(penteParams)
	} else {
		// Note: public hooks aren't really useful except in testing, as they disclose everything
		// TODO: remove this?
		paramsJSON, err = json.Marshal(params)
	}
	if err != nil {
		return nil, err
	}

	return &TransactionWrapper{
		transactionType: transactionType,
		functionABI:     functionABI,
		paramsJSON:      paramsJSON,
		contractAddress: &tx.DomainConfig.NotaryAddress,
	}, nil
}

func (h *burnHandler) Prepare(ctx context.Context, tx *types.ParsedTransaction, req *prototk.PrepareTransactionRequest) (*prototk.PrepareTransactionResponse, error) {
	baseTransaction, err := h.baseLedgerBurn(ctx, req)
	if err != nil {
		return nil, err
	}
	if tx.DomainConfig.NotaryType == types.NotaryTypePente {
		hookTransaction, err := h.hookBurn(ctx, tx, req, baseTransaction)
		if err != nil {
			return nil, err
		}
		return hookTransaction.prepare(nil)
	}
	return baseTransaction.prepare(nil)
}
