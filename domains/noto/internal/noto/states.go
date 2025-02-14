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
	"slices"

	"github.com/hyperledger/firefly-signer/pkg/eip712"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/kaleido-io/paladin/domains/noto/internal/msgs"
	"github.com/kaleido-io/paladin/domains/noto/pkg/types"
	"github.com/kaleido-io/paladin/toolkit/pkg/i18n"
	"github.com/kaleido-io/paladin/toolkit/pkg/log"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
)

var EIP712DomainName = "noto"
var EIP712DomainVersion = "0.0.1"
var EIP712DomainType = eip712.Type{
	{Name: "name", Type: "string"},
	{Name: "version", Type: "string"},
	{Name: "chainId", Type: "uint256"},
	{Name: "verifyingContract", Type: "address"},
}

var NotoCoinType = eip712.Type{
	{Name: "salt", Type: "bytes32"},
	{Name: "owner", Type: "address"},
	{Name: "amount", Type: "uint256"},
}

var NotoLockedCoinType = eip712.Type{
	{Name: "salt", Type: "bytes32"},
	{Name: "lockId", Type: "bytes32"},
	{Name: "owner", Type: "address"},
	{Name: "amount", Type: "uint256"},
}

var NotoTransferUnmaskedTypeSet = eip712.TypeSet{
	"Transfer": {
		{Name: "inputs", Type: "Coin[]"},
		{Name: "outputs", Type: "Coin[]"},
	},
	"Coin":              NotoCoinType,
	eip712.EIP712Domain: EIP712DomainType,
}

var NotoTransferMaskedTypeSet = eip712.TypeSet{
	"Transfer": {
		{Name: "inputs", Type: "bytes32[]"},
		{Name: "outputs", Type: "bytes32[]"},
		{Name: "data", Type: "bytes"},
	},
	eip712.EIP712Domain: EIP712DomainType,
}

var NotoLockTypeSet = eip712.TypeSet{
	"Lock": {
		{Name: "inputs", Type: "Coin[]"},
		{Name: "outputs", Type: "Coin[]"},
		{Name: "lockedOutputs", Type: "LockedCoin[]"},
	},
	"LockedCoin":        NotoLockedCoinType,
	"Coin":              NotoCoinType,
	eip712.EIP712Domain: EIP712DomainType,
}

var NotoUnlockTypeSet = eip712.TypeSet{
	"Unlock": {
		{Name: "lockedInputs", Type: "LockedCoin[]"},
		{Name: "lockedOutputs", Type: "LockedCoin[]"},
		{Name: "outputs", Type: "Coin[]"},
	},
	"LockedCoin":        NotoLockedCoinType,
	"Coin":              NotoCoinType,
	eip712.EIP712Domain: EIP712DomainType,
}

var NotoUnlockMaskedTypeSet = eip712.TypeSet{
	"Unlock": {
		{Name: "lockedInputs", Type: "bytes32[]"},
		{Name: "lockedOutputs", Type: "bytes32[]"},
		{Name: "outputs", Type: "bytes32[]"},
		{Name: "data", Type: "bytes"},
	},
	eip712.EIP712Domain: EIP712DomainType,
}

var NotoDelegateLockTypeSet = eip712.TypeSet{
	"DelegateLock": {
		{Name: "lockId", Type: "bytes32"},
		{Name: "delegate", Type: "address"},
		{Name: "data", Type: "bytes"},
	},
	eip712.EIP712Domain: EIP712DomainType,
}

func (n *Noto) unmarshalCoin(stateData string) (*types.NotoCoin, error) {
	var coin types.NotoCoin
	err := json.Unmarshal([]byte(stateData), &coin)
	return &coin, err
}

func (n *Noto) unmarshalLockedCoin(stateData string) (*types.NotoLockedCoin, error) {
	var coin types.NotoLockedCoin
	err := json.Unmarshal([]byte(stateData), &coin)
	return &coin, err
}

func (n *Noto) unmarshalInfo(stateData string) (*types.TransactionData, error) {
	var info types.TransactionData
	err := json.Unmarshal([]byte(stateData), &info)
	return &info, err
}

func (n *Noto) unmarshalLock(stateData string) (*types.NotoLockInfo, error) {
	var lock types.NotoLockInfo
	err := json.Unmarshal([]byte(stateData), &lock)
	return &lock, err
}

func (n *Noto) makeNewCoinState(coin *types.NotoCoin, distributionList []string) (*prototk.NewState, error) {
	coinJSON, err := json.Marshal(coin)
	if err != nil {
		return nil, err
	}
	return &prototk.NewState{
		SchemaId:         n.coinSchema.Id,
		StateDataJson:    string(coinJSON),
		DistributionList: distributionList,
	}, nil
}

func (n *Noto) makeNewLockedCoinState(coin *types.NotoLockedCoin, distributionList []string) (*prototk.NewState, error) {
	coinJSON, err := json.Marshal(coin)
	if err != nil {
		return nil, err
	}
	return &prototk.NewState{
		SchemaId:         n.lockedCoinSchema.Id,
		StateDataJson:    string(coinJSON),
		DistributionList: distributionList,
	}, nil
}

func (n *Noto) makeNewInfoState(info *types.TransactionData, distributionList []string) (*prototk.NewState, error) {
	infoJSON, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	return &prototk.NewState{
		SchemaId:         n.dataSchema.Id,
		StateDataJson:    string(infoJSON),
		DistributionList: distributionList,
	}, nil
}

func (n *Noto) makeNewLockState(lock *types.NotoLockInfo, distributionList []string) (*prototk.NewState, error) {
	lockJSON, err := json.Marshal(lock)
	if err != nil {
		return nil, err
	}
	return &prototk.NewState{
		SchemaId:         n.lockInfoSchema.Id,
		StateDataJson:    string(lockJSON),
		DistributionList: distributionList,
	}, nil
}

type preparedInputs struct {
	coins  []*types.NotoCoin
	states []*prototk.StateRef
	total  *big.Int
}

type preparedLockedInputs struct {
	coins  []*types.NotoLockedCoin
	states []*prototk.StateRef
	total  *big.Int
}

type preparedOutputs struct {
	coins  []*types.NotoCoin
	states []*prototk.NewState
}

type preparedLockedOutputs struct {
	coins  []*types.NotoLockedCoin
	states []*prototk.NewState
}

func (n *Noto) prepareInputs(ctx context.Context, stateQueryContext string, owner *tktypes.EthAddress, amount *tktypes.HexUint256) (inputs *preparedInputs, revert bool, err error) {
	var lastStateTimestamp int64
	total := big.NewInt(0)
	stateRefs := []*prototk.StateRef{}
	coins := []*types.NotoCoin{}
	for {
		// TODO: make this configurable
		queryBuilder := query.NewQueryBuilder().
			Limit(10).
			Sort(".created").
			Equal("owner", owner.String())

		if lastStateTimestamp > 0 {
			queryBuilder.GreaterThan(".created", lastStateTimestamp)
		}

		log.L(ctx).Debugf("State query: %s", queryBuilder.Query())
		states, err := n.findAvailableStates(ctx, stateQueryContext, n.coinSchema.Id, queryBuilder.Query().String())
		if err != nil {
			return nil, false, err
		}
		if len(states) == 0 {
			return nil, true, i18n.NewError(ctx, msgs.MsgInsufficientFunds, total.Text(10))
		}
		for _, state := range states {
			lastStateTimestamp = state.CreatedAt
			coin, err := n.unmarshalCoin(state.DataJson)
			if err != nil {
				return nil, false, i18n.NewError(ctx, msgs.MsgInvalidStateData, state.Id, err)
			}
			total = total.Add(total, coin.Amount.Int())
			stateRefs = append(stateRefs, &prototk.StateRef{
				SchemaId: state.SchemaId,
				Id:       state.Id,
			})
			coins = append(coins, coin)
			log.L(ctx).Debugf("Selecting coin %s value=%s total=%s required=%s)", state.Id, coin.Amount.Int().Text(10), total.Text(10), amount.Int().Text(10))
			if total.Cmp(amount.Int()) >= 0 {
				return &preparedInputs{
					coins:  coins,
					states: stateRefs,
					total:  total,
				}, false, nil
			}
		}
	}
}

func (n *Noto) prepareLockedInputs(ctx context.Context, stateQueryContext string, lockID tktypes.Bytes32, owner *tktypes.EthAddress, amount *big.Int) (inputs *preparedLockedInputs, revert bool, err error) {
	var lastStateTimestamp int64
	total := big.NewInt(0)
	stateRefs := []*prototk.StateRef{}
	coins := []*types.NotoLockedCoin{}

	for {
		queryBuilder := query.NewQueryBuilder().
			Limit(10).
			Sort(".created").
			Equal("lockId", lockID).
			Equal("owner", owner.String())

		if lastStateTimestamp > 0 {
			queryBuilder.GreaterThan(".created", lastStateTimestamp)
		}

		log.L(ctx).Debugf("State query: %s", queryBuilder.Query())
		states, err := n.findAvailableStates(ctx, stateQueryContext, n.lockedCoinSchema.Id, queryBuilder.Query().String())

		if err != nil {
			return nil, false, err
		}
		if len(states) == 0 {
			return nil, true, i18n.NewError(ctx, msgs.MsgInsufficientFunds, total.Text(10))
		}
		for _, state := range states {
			lastStateTimestamp = state.CreatedAt
			coin, err := n.unmarshalLockedCoin(state.DataJson)
			if err != nil {
				return nil, false, i18n.NewError(ctx, msgs.MsgInvalidStateData, state.Id, err)
			}
			total = total.Add(total, coin.Amount.Int())
			stateRefs = append(stateRefs, &prototk.StateRef{
				SchemaId: state.SchemaId,
				Id:       state.Id,
			})
			coins = append(coins, coin)
			log.L(ctx).Debugf("Selecting coin %s value=%s total=%s required=%s)", state.Id, coin.Amount.Int().Text(10), total.Text(10), amount.Text(10))
			if total.Cmp(amount) >= 0 {
				return &preparedLockedInputs{
					coins:  coins,
					states: stateRefs,
					total:  total,
				}, false, nil
			}
		}
	}
}

func (n *Noto) prepareOutputs(ownerAddress *tktypes.EthAddress, amount *tktypes.HexUint256, distributionList []string) (*preparedOutputs, error) {
	// Always produce a single coin for the entire output amount
	// TODO: make this configurable
	newCoin := &types.NotoCoin{
		Salt:   tktypes.RandBytes32(),
		Owner:  ownerAddress,
		Amount: amount,
	}
	newState, err := n.makeNewCoinState(newCoin, distributionList)
	return &preparedOutputs{
		coins:  []*types.NotoCoin{newCoin},
		states: []*prototk.NewState{newState},
	}, err
}

func (n *Noto) prepareLockedOutputs(id tktypes.Bytes32, ownerAddress *tktypes.EthAddress, amount *tktypes.HexUint256, distributionList []string) (*preparedLockedOutputs, error) {
	// Always produce a single coin for the entire output amount
	// TODO: make this configurable
	newCoin := &types.NotoLockedCoin{
		Salt:   tktypes.RandBytes32(),
		LockID: id,
		Owner:  ownerAddress,
		Amount: amount,
	}
	newState, err := n.makeNewLockedCoinState(newCoin, distributionList)
	return &preparedLockedOutputs{
		coins:  []*types.NotoLockedCoin{newCoin},
		states: []*prototk.NewState{newState},
	}, err
}

func (n *Noto) prepareInfo(data tktypes.HexBytes, distributionList []string) ([]*prototk.NewState, error) {
	newData := &types.TransactionData{
		Salt: tktypes.RandHex(32),
		Data: data,
	}
	newState, err := n.makeNewInfoState(newData, distributionList)
	return []*prototk.NewState{newState}, err
}

func (n *Noto) prepareLockInfo(lockID tktypes.Bytes32, owner, delegate *tktypes.EthAddress, distributionList []string) (*prototk.NewState, error) {
	if delegate == nil {
		delegate = &tktypes.EthAddress{}
	}
	newData := &types.NotoLockInfo{
		Salt:     tktypes.RandBytes32(),
		LockID:   lockID,
		Owner:    owner,
		Delegate: delegate,
	}
	return n.makeNewLockState(newData, distributionList)

}

func (n *Noto) filterSchema(states []*prototk.EndorsableState, schemas []string) (filtered []*prototk.EndorsableState) {
	for _, state := range states {
		if slices.Contains(schemas, state.SchemaId) {
			filtered = append(filtered, state)
		}
	}
	return filtered
}

func (n *Noto) splitStates(states []*prototk.EndorsableState) (unlocked []*prototk.EndorsableState, locked []*prototk.EndorsableState) {
	return n.filterSchema(states, []string{n.coinSchema.Id}), n.filterSchema(states, []string{n.lockedCoinSchema.Id})
}

func (n *Noto) getStates(ctx context.Context, stateQueryContext, schemaId string, ids []string) ([]*prototk.StoredState, error) {
	req := &prototk.GetStatesByIDRequest{
		StateQueryContext: stateQueryContext,
		SchemaId:          schemaId,
		StateIds:          ids,
	}
	res, err := n.Callbacks.GetStatesByID(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.States, nil
}

func (n *Noto) findAvailableStates(ctx context.Context, stateQueryContext, schemaId, query string) ([]*prototk.StoredState, error) {
	req := &prototk.FindAvailableStatesRequest{
		StateQueryContext: stateQueryContext,
		SchemaId:          schemaId,
		QueryJson:         query,
	}
	res, err := n.Callbacks.FindAvailableStates(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.States, nil
}

func (n *Noto) eip712Domain(contract *ethtypes.Address0xHex) map[string]any {
	return map[string]any{
		"name":              EIP712DomainName,
		"version":           EIP712DomainVersion,
		"chainId":           n.chainID,
		"verifyingContract": contract,
	}
}

func (n *Noto) encodeNotoCoins(coins []*types.NotoCoin) []any {
	encodedCoins := make([]any, len(coins))
	for i, coin := range coins {
		encodedCoins[i] = map[string]any{
			"salt":   coin.Salt,
			"owner":  coin.Owner,
			"amount": coin.Amount.String(),
		}
	}
	return encodedCoins
}

func (n *Noto) encodeNotoLockedCoins(coins []*types.NotoLockedCoin) []any {
	encodedCoins := make([]any, len(coins))
	for i, coin := range coins {
		encodedCoins[i] = map[string]any{
			"salt":   coin.Salt,
			"lockId": coin.LockID,
			"owner":  coin.Owner,
			"amount": coin.Amount.String(),
		}
	}
	return encodedCoins
}

func encodedStateIDs(states []*pldapi.StateEncoded) []string {
	inputs := make([]string, len(states))
	for i, state := range states {
		inputs[i] = state.ID.String()
	}
	return inputs
}

func endorsableStateIDs(states []*prototk.EndorsableState) []string {
	inputs := make([]string, len(states))
	for i, state := range states {
		inputs[i] = state.Id
	}
	return inputs
}

func stringToAny(ids []string) []any {
	result := make([]any, len(ids))
	for i, id := range ids {
		result[i] = id
	}
	return result
}

func (n *Noto) encodeTransferUnmasked(ctx context.Context, contract *ethtypes.Address0xHex, inputs, outputs []*types.NotoCoin) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoTransferUnmaskedTypeSet,
		PrimaryType: "Transfer",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"inputs":  n.encodeNotoCoins(inputs),
			"outputs": n.encodeNotoCoins(outputs),
		},
	})
}

func (n *Noto) encodeTransferMasked(ctx context.Context, contract *ethtypes.Address0xHex, inputs, outputs []*pldapi.StateEncoded, data tktypes.HexBytes) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoTransferMaskedTypeSet,
		PrimaryType: "Transfer",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"inputs":  stringToAny(encodedStateIDs(inputs)),
			"outputs": stringToAny(encodedStateIDs(outputs)),
			"data":    data,
		},
	})
}

func (n *Noto) encodeLock(ctx context.Context, contract *ethtypes.Address0xHex, inputs, outputs []*types.NotoCoin, lockedOutputs []*types.NotoLockedCoin) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoLockTypeSet,
		PrimaryType: "Lock",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"inputs":        n.encodeNotoCoins(inputs),
			"outputs":       n.encodeNotoCoins(outputs),
			"lockedOutputs": n.encodeNotoLockedCoins(lockedOutputs),
		},
	})
}

func (n *Noto) encodeUnlock(ctx context.Context, contract *ethtypes.Address0xHex, lockedInputs, lockedOutputs []*types.NotoLockedCoin, outputs []*types.NotoCoin) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoUnlockTypeSet,
		PrimaryType: "Unlock",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"lockedInputs":  n.encodeNotoLockedCoins(lockedInputs),
			"lockedOutputs": n.encodeNotoLockedCoins(lockedOutputs),
			"outputs":       n.encodeNotoCoins(outputs),
		},
	})
}

func (n *Noto) unlockHashFromStates(ctx context.Context, contract *ethtypes.Address0xHex, lockedInputs, lockedOutputs, outputs []*prototk.EndorsableState, data tktypes.HexBytes) (ethtypes.HexBytes0xPrefix, error) {
	return n.unlockHashFromIDs(ctx, contract, endorsableStateIDs(lockedInputs), endorsableStateIDs(lockedOutputs), endorsableStateIDs(outputs), data)
}

func (n *Noto) unlockHashFromIDs(ctx context.Context, contract *ethtypes.Address0xHex, lockedInputs, lockedOutputs, outputs []string, data tktypes.HexBytes) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoUnlockMaskedTypeSet,
		PrimaryType: "Unlock",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"lockedInputs":  stringToAny(lockedInputs),
			"lockedOutputs": stringToAny(lockedOutputs),
			"outputs":       stringToAny(outputs),
			"data":          data,
		},
	})
}

func (n *Noto) encodeDelegateLock(ctx context.Context, contract *ethtypes.Address0xHex, lockID tktypes.Bytes32, delegate *tktypes.EthAddress, data tktypes.HexBytes) (ethtypes.HexBytes0xPrefix, error) {
	return eip712.EncodeTypedDataV4(ctx, &eip712.TypedData{
		Types:       NotoDelegateLockTypeSet,
		PrimaryType: "DelegateLock",
		Domain:      n.eip712Domain(contract),
		Message: map[string]any{
			"lockId":   lockID,
			"delegate": delegate,
			"data":     data,
		},
	})
}
