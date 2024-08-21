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

package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/hyperledger/firefly-common/pkg/log"
	"github.com/hyperledger/firefly-signer/pkg/abi"
	"github.com/kaleido-io/paladin/kata/internal/cache"
	"github.com/kaleido-io/paladin/kata/internal/filters"
	"github.com/kaleido-io/paladin/kata/internal/msgs"
	"github.com/kaleido-io/paladin/kata/internal/plugins"
	"github.com/kaleido-io/paladin/kata/internal/statestore"
	"github.com/kaleido-io/paladin/kata/pkg/types"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/retry"
	"gopkg.in/yaml.v3"
)

type domain struct {
	ctx       context.Context
	cancelCtx context.CancelFunc

	conf          *DomainConfig
	dm            *domainManager
	id            uuid.UUID
	name          string
	api           plugins.DomainManagerToDomain
	contractCache cache.Cache[types.EthAddress, *domainContract]

	stateLock              sync.Mutex
	initialized            atomic.Bool
	initRetry              *retry.Retry
	config                 *prototk.DomainConfig
	schemasBySignature     map[string]statestore.Schema
	schemasByID            map[string]statestore.Schema
	constructorABI         *abi.Entry
	factoryContractAddress *types.EthAddress
	factoryContractABI     abi.ABI
	privateContractABI     abi.ABI

	initError atomic.Pointer[error]
	initDone  chan struct{}
}

func (dm *domainManager) newDomain(id uuid.UUID, name string, conf *DomainConfig, toDomain plugins.DomainManagerToDomain) *domain {
	d := &domain{
		dm:            dm,
		conf:          conf,
		initRetry:     retry.NewRetryIndefinite(&conf.Init.Retry),
		name:          name,
		id:            id,
		api:           toDomain,
		initDone:      make(chan struct{}),
		contractCache: cache.NewCache[types.EthAddress, *domainContract](&conf.ContractCache, ContractCacheDefaults),

		schemasByID:        make(map[string]statestore.Schema),
		schemasBySignature: make(map[string]statestore.Schema),
	}
	d.ctx, d.cancelCtx = context.WithCancel(log.WithLogField(dm.bgCtx, "domain", d.name))
	return d
}

func (d *domain) processDomainConfig(confRes *prototk.ConfigureDomainResponse) (*prototk.InitDomainRequest, error) {
	d.stateLock.Lock()
	defer d.stateLock.Unlock()

	// Parse all the schemas
	d.config = confRes.DomainConfig
	abiSchemas := make([]*abi.Parameter, len(d.config.AbiStateSchemasJson))
	for i, schemaJSON := range d.config.AbiStateSchemasJson {
		if err := json.Unmarshal([]byte(schemaJSON), &abiSchemas[i]); err != nil {
			return nil, i18n.WrapError(d.ctx, err, msgs.MsgDomainInvalidSchema, i)
		}
	}

	err := json.Unmarshal(([]byte)(d.config.ConstructorAbiJson), &d.constructorABI)
	if err != nil {
		return nil, i18n.WrapError(d.ctx, err, msgs.MsgDomainConstructorAbiJsonInvalid)
	}
	if d.constructorABI.Type != abi.Constructor {
		return nil, i18n.NewError(d.ctx, msgs.MsgDomainConstructorABITypeWrong, d.constructorABI.Type)
	}

	if err := json.Unmarshal(([]byte)(d.config.FactoryContractAbiJson), &d.factoryContractABI); err != nil {
		return nil, i18n.WrapError(d.ctx, err, msgs.MsgDomainFactoryAbiJsonInvalid)
	}

	if err := json.Unmarshal(([]byte)(d.config.PrivateContractAbiJson), &d.privateContractABI); err != nil {
		return nil, i18n.WrapError(d.ctx, err, msgs.MsgDomainPrivateAbiJsonInvalid)
	}

	d.factoryContractAddress, err = types.ParseEthAddress(d.config.FactoryContractAddress)
	if err != nil {
		return nil, i18n.WrapError(d.ctx, err, msgs.MsgDomainFactoryAddressInvalid)
	}

	// Ensure all the schemas are recorded to the DB
	// This is a special case where we need a synchronous flush to ensure they're all established
	var schemas []statestore.Schema
	err = d.dm.stateStore.RunInDomainContextFlush(d.name, func(ctx context.Context, dsi statestore.DomainStateInterface) (err error) {
		schemas, err = dsi.EnsureABISchemas(abiSchemas)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Build the request to the init
	schemasProto := make([]*prototk.StateSchema, len(schemas))
	for i, s := range schemas {
		schemaID := s.IDString()
		d.schemasByID[schemaID] = s
		d.schemasBySignature[s.Signature()] = s
		schemasProto[i] = &prototk.StateSchema{
			Id:        schemaID,
			Signature: s.Signature(),
		}
	}
	return &prototk.InitDomainRequest{
		DomainUuid:      d.id.String(),
		AbiStateSchemas: schemasProto,
	}, nil
}

func (d *domain) init() {
	defer close(d.initDone)

	// We block retrying each part of init until we succeed, or are cancelled
	// (which the plugin manager will do if the domain disconnects)
	err := d.initRetry.Do(d.ctx, func(attempt int) (bool, error) {

		// Send the configuration to the domain for processing
		confYAML, _ := yaml.Marshal(&d.conf.Config)
		confRes, err := d.api.ConfigureDomain(d.ctx, &prototk.ConfigureDomainRequest{
			Name:       d.name,
			ChainId:    d.dm.chainID,
			ConfigYaml: string(confYAML),
		})
		if err != nil {
			return true, err
		}

		// Process the configuration, so we can move onto init
		initReq, err := d.processDomainConfig(confRes)
		if err != nil {
			return true, err
		}

		// Complete the initialization
		_, err = d.api.InitDomain(d.ctx, initReq)
		return true, err
	})
	if err != nil {
		log.L(d.ctx).Debugf("domain initialization cancelled before completion: %s", err)
		d.initError.Store(&err)
	} else {
		log.L(d.ctx).Debugf("domain initialization complete")
		d.dm.setDomainAddress(d)
		d.initialized.Store(true)
		// Inform the plugin manager callback
		d.api.Initialized()
	}
}

func (d *domain) checkInit(ctx context.Context) error {
	if !d.initialized.Load() {
		return i18n.NewError(ctx, msgs.MsgDomainNotInitialized)
	}
	return nil
}

// Domain callback to query the state store
func (d *domain) FindAvailableStates(ctx context.Context, req *prototk.FindAvailableStatesRequest) (*prototk.FindAvailableStatesResponse, error) {
	if err := d.checkInit(ctx); err != nil {
		return nil, err
	}

	var query filters.QueryJSON
	err := json.Unmarshal([]byte(req.QueryJson), &query)
	if err != nil {
		return nil, i18n.WrapError(ctx, err, msgs.MsgDomainInvalidQueryJSON)
	}

	var states []*statestore.State
	err = d.dm.stateStore.RunInDomainContext(d.name, func(ctx context.Context, dsi statestore.DomainStateInterface) (err error) {
		states, err = dsi.FindAvailableStates(req.SchemaId, &query)
		return err
	})
	if err != nil {
		return nil, err
	}

	pbStates := make([]*prototk.StoredState, len(states))
	for i, s := range states {
		pbStates[i] = &prototk.StoredState{
			HashId:   s.ID.String(),
			SchemaId: s.Schema.String(),
			StoredAt: s.CreatedAt.UnixNano(),
			DataJson: string(s.Data),
		}
		if s.Locked != nil {
			pbStates[i].Lock = &prototk.StateLock{
				Sequence: s.Locked.Sequence.String(),
				Creating: s.Locked.Creating,
				Spending: s.Locked.Spending,
			}
		}
	}
	return &prototk.FindAvailableStatesResponse{
		States: pbStates,
	}, nil

}

func (d *domain) close() {
	d.cancelCtx()
	<-d.initDone
}
