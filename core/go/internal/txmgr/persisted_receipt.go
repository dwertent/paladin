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

package txmgr

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/hyperledger/firefly-signer/pkg/abi"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/kaleido-io/paladin/core/internal/components"
	"github.com/kaleido-io/paladin/core/internal/filters"
	"github.com/kaleido-io/paladin/core/internal/msgs"
	"github.com/kaleido-io/paladin/toolkit/pkg/log"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type transactionReceipt struct {
	TransactionID    uuid.UUID           `gorm:"column:transaction"`
	Indexed          tktypes.Timestamp   `gorm:"column:indexed"`
	Domain           string              `gorm:"column:domain"`
	Success          bool                `gorm:"column:success"`
	TransactionHash  *tktypes.Bytes32    `gorm:"column:tx_hash"`
	BlockNumber      *int64              `gorm:"column:block_number"`
	TransactionIndex *int64              `gorm:"column:tx_index"`
	LogIndex         *int64              `gorm:"column:log_index"`
	Source           *tktypes.EthAddress `gorm:"column:source"`
	FailureMessage   *string             `gorm:"column:failure_message"`
	RevertData       tktypes.HexBytes    `gorm:"column:revert_data"`
	ContractAddress  *tktypes.EthAddress `gorm:"column:contract_address"`
}

func mapPersistedReceipt(receipt *transactionReceipt) *pldapi.TransactionReceiptData {
	r := &pldapi.TransactionReceiptData{
		Domain:          receipt.Domain,
		Success:         receipt.Success,
		FailureMessage:  stringOrEmpty(receipt.FailureMessage),
		RevertData:      receipt.RevertData,
		ContractAddress: receipt.ContractAddress,
	}
	if receipt.TransactionHash != nil {
		r.TransactionReceiptDataOnchain = &pldapi.TransactionReceiptDataOnchain{
			TransactionHash:  receipt.TransactionHash,
			BlockNumber:      int64OrZero(receipt.BlockNumber),
			TransactionIndex: int64OrZero(receipt.TransactionIndex),
		}
	}
	if receipt.Source != nil {
		r.TransactionReceiptDataOnchainEvent = &pldapi.TransactionReceiptDataOnchainEvent{
			LogIndex: int64OrZero(receipt.LogIndex),
			Source:   *receipt.Source,
		}
	}

	return r
}

var transactionReceiptFilters = filters.FieldMap{
	"id":              filters.UUIDField(`"transaction"`),
	"indexed":         filters.TimestampField("indexed"),
	"success":         filters.BooleanField("success"),
	"transactionHash": filters.HexBytesField("tx_hash"),
	"blockNumber":     filters.Int64Field("block_number"),
}

// FinalizeTransactions is called by the block indexing routine, but also can be called
// by the private transaction manager if transactions fail without making it to the blockchain
func (tm *txManager) FinalizeTransactions(ctx context.Context, dbTX *gorm.DB, info []*components.ReceiptInput) error {

	if len(info) == 0 {
		return nil
	}

	receiptsToInsert := make([]*transactionReceipt, 0, len(info))
	for _, ri := range info {
		receipt := &transactionReceipt{
			Domain:          ri.Domain,
			TransactionID:   ri.TransactionID,
			Indexed:         tktypes.TimestampNow(),
			ContractAddress: ri.ContractAddress,
		}
		if ri.OnChain.Type != tktypes.NotOnChain {
			receipt.TransactionHash = &ri.OnChain.TransactionHash
			receipt.BlockNumber = &ri.OnChain.BlockNumber
			receipt.TransactionIndex = &ri.OnChain.TransactionIndex
			receipt.LogIndex = &ri.OnChain.LogIndex
			receipt.Source = ri.OnChain.Source
		}
		// Process each type, checking for coding errors in the calling component
		var failureMsg string
		switch ri.ReceiptType {
		case components.RT_Success:
			if ri.FailureMessage != "" || ri.RevertData != nil {
				return i18n.NewError(ctx, msgs.MsgTxMgrInvalidReceiptNotification, tktypes.JSONString(ri))
			}
			receipt.Success = true
		case components.RT_FailedWithMessage:
			if ri.FailureMessage == "" || ri.RevertData != nil {
				return i18n.NewError(ctx, msgs.MsgTxMgrInvalidReceiptNotification, tktypes.JSONString(ri))
			}
			receipt.Success = false
			failureMsg = ri.FailureMessage
			receipt.FailureMessage = &ri.FailureMessage
		case components.RT_FailedOnChainWithRevertData:
			if ri.FailureMessage != "" {
				return i18n.NewError(ctx, msgs.MsgTxMgrInvalidReceiptNotification, tktypes.JSONString(ri))
			}
			receipt.Success = false
			receipt.RevertData = ri.RevertData
			// We calculate the failure message - all errors handled mapped internally here
			failureMsg = tm.CalculateRevertError(ctx, dbTX, ri.RevertData).Error()
			receipt.FailureMessage = &failureMsg
		default:
			return i18n.NewError(ctx, msgs.MsgTxMgrInvalidReceiptNotification, tktypes.JSONString(ri))
		}
		log.L(ctx).Infof("Inserting receipt txId=%s success=%t failure=%s txHash=%v", receipt.TransactionID, receipt.Success, failureMsg, receipt.TransactionHash)
		receiptsToInsert = append(receiptsToInsert, receipt)
	}

	if len(receiptsToInsert) > 0 {
		err := dbTX.Table("transaction_receipts").
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "transaction"}},
				DoNothing: true, // once inserted, the receipt is immutable
			}).
			Create(receiptsToInsert).
			Error
		if err != nil {
			return err
		}
	}

	// TODO: Need to create an guaranteed increasing event table for these receipts, as applications
	//       must be able to efficiently and reliably listen for them as they are written (good or bad)

	return nil
}

func (tm *txManager) CalculateRevertError(ctx context.Context, dbTX *gorm.DB, revertData tktypes.HexBytes) error {
	de, err := tm.DecodeRevertError(ctx, dbTX, revertData, "")
	if err != nil {
		return err
	}
	return i18n.NewError(ctx, msgs.MsgTxMgrRevertedDecodedData, de.Summary)
}

func (tm *txManager) DecodeRevertError(ctx context.Context, dbTX *gorm.DB, revertData tktypes.HexBytes, dataFormat tktypes.JSONFormatOptions) (*pldapi.ABIDecodedData, error) {

	if len(revertData) < 4 {
		return nil, i18n.NewError(ctx, msgs.MsgTxMgrRevertedNoData)
	}
	selector := tktypes.HexBytes(revertData[0:4])

	// There is potential with a 4 byte selector for clashes, so we do a distinct on the full hash
	var errorDefs []*PersistedABIEntry
	err := dbTX.Table("abi_entries").
		Where("selector = ?", selector).
		Where("type = ?", abi.Error).
		Distinct("full_hash", "definition").
		Find(&errorDefs).
		Error
	if err != nil {
		return nil, i18n.WrapError(ctx, err, msgs.MsgTxMgrRevertedNoMatchingErrABI, revertData)
	}

	// Turn this into an ABI that we pass to the handy utility (which also includes
	// the default revert error) to decode for us.
	virtualABI := abi.ABI{}
	for _, def := range errorDefs {
		var e abi.Entry
		err := json.Unmarshal(def.Definition, &e)
		if err == nil {
			virtualABI = append(virtualABI, &e)
		}
	}
	e, cv, ok := virtualABI.ParseErrorCtx(ctx, revertData)
	if !ok {
		return nil, i18n.NewError(ctx, msgs.MsgTxMgrRevertedNoMatchingErrABI, revertData)
	}
	de := &pldapi.ABIDecodedData{
		Summary:    abi.FormatErrorStringCtx(ctx, e, cv),
		Definition: e,
		Signature:  e.String(),
	}
	serializer, err := dataFormat.GetABISerializer(ctx)
	if err == nil {
		de.Data, err = serializer.SerializeJSONCtx(ctx, cv)
	}
	if err != nil {
		return nil, err
	}
	return de, nil
}

func (tm *txManager) DecodeCall(ctx context.Context, dbTX *gorm.DB, callData tktypes.HexBytes, dataFormat tktypes.JSONFormatOptions) (*pldapi.ABIDecodedData, error) {

	if len(callData) < 4 {
		return nil, i18n.NewError(ctx, msgs.MsgTxMgrDecodeCallNoData)
	}
	selector := tktypes.HexBytes(callData[0:4])

	// There is potential with a 4 byte selector for clashes, so we do a distinct on the full hash
	var functionDefs []*PersistedABIEntry
	err := dbTX.Table("abi_entries").
		Where("selector = ?", selector).
		Where("type = ?", abi.Function).
		Distinct("full_hash", "definition").
		Find(&functionDefs).
		Error

	var e *abi.Entry
	var cv *abi.ComponentValue
	if err == nil {
		for _, storedDef := range functionDefs {
			_ = json.Unmarshal(storedDef.Definition, &e)
			if e != nil && e.Inputs != nil {
				cv, err = e.DecodeCallDataCtx(ctx, callData)
				if err == nil {
					break
				}
			}
		}
	}
	if cv == nil {
		return nil, i18n.WrapError(ctx, err, msgs.MsgTxMgrDecodeCallDataNoABI, len(functionDefs))
	}

	de := &pldapi.ABIDecodedData{
		Definition: e,
		Signature:  e.String(),
	}
	serializer, err := dataFormat.GetABISerializer(ctx)
	if err == nil {
		de.Data, err = serializer.SerializeJSONCtx(ctx, cv)
	}
	return de, err
}

func (tm *txManager) DecodeEvent(ctx context.Context, dbTX *gorm.DB, topics []tktypes.Bytes32, eventData tktypes.HexBytes, dataFormat tktypes.JSONFormatOptions) (*pldapi.ABIDecodedData, error) {

	if len(topics) < 1 {
		return nil, i18n.NewError(ctx, msgs.MsgTxMgrDecodeCallNoData)
	}
	ethTopics := make([]ethtypes.HexBytes0xPrefix, len(topics))
	for i, t := range topics {
		ethTopics[i] = t[:]
	}

	var eventDefs []*PersistedABIEntry
	err := dbTX.Table("abi_entries").
		Where("full_hash = ?", topics[0]).
		Where("type = ?", abi.Event).
		Find(&eventDefs).
		Error

	var e *abi.Entry
	var cv *abi.ComponentValue
	if err == nil {
		for _, storedDef := range eventDefs {
			_ = json.Unmarshal(storedDef.Definition, &e)
			if e != nil && e.Inputs != nil {
				cv, err = e.DecodeEventDataCtx(ctx, ethTopics, ethtypes.HexBytes0xPrefix(eventData))
				if err == nil {
					break
				}
			}
		}
	}
	if cv == nil {
		return nil, i18n.WrapError(ctx, err, msgs.MsgTxMgrDecodeEventNoABI, len(eventDefs))
	}

	de := &pldapi.ABIDecodedData{
		Definition: e,
		Signature:  e.String(),
	}
	serializer, err := dataFormat.GetABISerializer(ctx)
	if err == nil {
		de.Data, err = serializer.SerializeJSONCtx(ctx, cv)
	}
	return de, err
}

func (tm *txManager) QueryTransactionReceipts(ctx context.Context, jq *query.QueryJSON) ([]*pldapi.TransactionReceipt, error) {
	qw := &queryWrapper[transactionReceipt, pldapi.TransactionReceipt]{
		p:           tm.p,
		table:       "transaction_receipts",
		defaultSort: "-indexed",
		filters:     transactionReceiptFilters,
		query:       jq,
		mapResult: func(pt *transactionReceipt) (*pldapi.TransactionReceipt, error) {
			return &pldapi.TransactionReceipt{
				ID:                     pt.TransactionID,
				TransactionReceiptData: *mapPersistedReceipt(pt),
			}, nil
		},
	}
	return qw.run(ctx, nil)
}

func (tm *txManager) GetTransactionReceiptByID(ctx context.Context, id uuid.UUID) (*pldapi.TransactionReceipt, error) {
	prs, err := tm.QueryTransactionReceipts(ctx, query.NewQueryBuilder().Limit(1).Equal("id", id).Query())
	if len(prs) == 0 || err != nil {
		return nil, err
	}
	return prs[0], nil
}

func (tm *txManager) GetTransactionReceiptByIDFull(ctx context.Context, id uuid.UUID) (*pldapi.TransactionReceiptFull, error) {
	receipt, err := tm.GetTransactionReceiptByID(ctx, id)
	if err != nil || receipt == nil {
		return nil, err
	}
	fullReceipt := &pldapi.TransactionReceiptFull{TransactionReceipt: receipt}
	if receipt.Domain != "" {
		fullReceipt.States, err = tm.stateMgr.GetTransactionStates(ctx, tm.p.DB(), id)
		if err == nil {
			d, domainErr := tm.domainMgr.GetDomainByName(ctx, receipt.Domain)
			if domainErr == nil {
				fullReceipt.DomainReceipt, domainErr = d.BuildDomainReceipt(ctx, tm.p.DB(), id, fullReceipt.States)
			}
			if domainErr != nil {
				fullReceipt.DomainReceiptError = domainErr.Error()
			}
		}
	}
	return fullReceipt, nil
}

func (tm *txManager) GetDomainReceiptByID(ctx context.Context, domain string, id uuid.UUID) (tktypes.RawJSON, error) {
	d, err := tm.domainMgr.GetDomainByName(ctx, domain)
	if err != nil {
		return nil, err
	}
	return d.GetDomainReceipt(ctx, tm.p.DB(), id)
}

func (tm *txManager) GetStateReceiptByID(ctx context.Context, id uuid.UUID) (*pldapi.TransactionStates, error) {
	return tm.stateMgr.GetTransactionStates(ctx, tm.p.DB(), id)
}