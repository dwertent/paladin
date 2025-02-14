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

package statemgr

import (
	"context"

	"github.com/kaleido-io/paladin/core/internal/components"
	"github.com/kaleido-io/paladin/core/pkg/persistence"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/rpcserver"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
)

func (ss *stateManager) RPCModule() *rpcserver.RPCModule {
	return ss.rpcModule
}

func (ss *stateManager) initRPC() {
	ss.rpcModule = rpcserver.NewRPCModule("pstate").
		Add("pstate_listSchemas", ss.rpcListSchema()).
		Add("pstate_storeState", ss.rpcStoreState()).
		Add("pstate_queryStates", ss.rpcQueryStates()).
		Add("pstate_queryContractStates", ss.rpcQueryContractStates()).
		Add("pstate_queryNullifiers", ss.rpcQueryNullifiers()).
		Add("pstate_queryContractNullifiers", ss.rpcQueryContractNullifiers())
}

func (ss *stateManager) rpcListSchema() rpcserver.RPCHandler {
	return rpcserver.RPCMethod1(func(ctx context.Context,
		domain string,
	) ([]*pldapi.Schema, error) {
		return ss.ListSchemasForJSON(ctx, ss.p.NOTX(), domain)
	})
}

func (ss *stateManager) rpcStoreState() rpcserver.RPCHandler {
	return rpcserver.RPCMethod4(func(ctx context.Context,
		domain string,
		contractAddress tktypes.EthAddress,
		schema tktypes.Bytes32,
		data tktypes.RawJSON,
	) (*pldapi.State, error) {
		var state *pldapi.State
		err := ss.p.Transaction(ctx, func(ctx context.Context, dbTX persistence.DBTX) error {
			newStates, err := ss.WriteReceivedStates(ctx, dbTX, domain, []*components.StateUpsertOutsideContext{
				{
					ContractAddress: contractAddress,
					SchemaID:        schema,
					Data:            data,
				},
			})
			if err == nil {
				state = newStates[0]
			}
			return err
		})
		return state, err
	})
}

func (ss *stateManager) rpcQueryStates() rpcserver.RPCHandler {
	return rpcserver.RPCMethod4(func(ctx context.Context,
		domain string,
		schema tktypes.Bytes32,
		query query.QueryJSON,
		status pldapi.StateStatusQualifier,
	) ([]*pldapi.State, error) {
		return ss.FindStates(ctx, ss.p.NOTX(), domain, schema, &query, status)
	})
}

func (ss *stateManager) rpcQueryContractStates() rpcserver.RPCHandler {
	return rpcserver.RPCMethod5(func(ctx context.Context,
		domain string,
		contractAddress tktypes.EthAddress,
		schema tktypes.Bytes32,
		query query.QueryJSON,
		status pldapi.StateStatusQualifier,
	) ([]*pldapi.State, error) {
		return ss.FindContractStates(ctx, ss.p.NOTX(), domain, contractAddress, schema, &query, status)
	})
}

func (ss *stateManager) rpcQueryNullifiers() rpcserver.RPCHandler {
	return rpcserver.RPCMethod4(func(ctx context.Context,
		domain string,
		schema tktypes.Bytes32,
		query query.QueryJSON,
		status pldapi.StateStatusQualifier,
	) ([]*pldapi.State, error) {
		return ss.FindNullifiers(ctx, ss.p.NOTX(), domain, schema, &query, status)
	})
}

func (ss *stateManager) rpcQueryContractNullifiers() rpcserver.RPCHandler {
	return rpcserver.RPCMethod5(func(ctx context.Context,
		domain string,
		contractAddress tktypes.EthAddress,
		schema tktypes.Bytes32,
		query query.QueryJSON,
		status pldapi.StateStatusQualifier,
	) ([]*pldapi.State, error) {
		return ss.FindContractNullifiers(ctx, ss.p.NOTX(), domain, contractAddress, schema, &query, status)
	})
}
