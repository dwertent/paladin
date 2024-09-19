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

package components

import (
	"context"
	"math/big"

	"github.com/google/uuid"
	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/hyperledger/firefly-signer/pkg/ethsigner"
	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/kaleido-io/paladin/core/internal/msgs"
	"github.com/kaleido-io/paladin/core/pkg/blockindexer"
	"github.com/kaleido-io/paladin/core/pkg/ethclient"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
)

// PublicTransactionEventType is a enum type that contains all types of transaction process events
// that a transaction handler emits.
type PublicTransactionEventType int

const (
	PublicTXProcessSucceeded PublicTransactionEventType = iota
	PublicTXProcessFailed
)

// PubTxStatus is the current status of a transaction
type PubTxStatus string

func (ro *RequestOptions) Validate(ctx context.Context) error {
	if ro.ID == nil {
		return i18n.NewError(ctx, msgs.MsgMissingTransactionID)
	}

	if ro.SignerID == "" {
		return i18n.NewError(ctx, msgs.MsgErrorMissingSignerID)
	}
	return nil
}

const (
	// PubTxStatusPending indicates the operation has been submitted, but is not yet confirmed as successful or failed
	PubTxStatusPending PubTxStatus = "Pending"
	// PubTxStatusSucceeded the infrastructure runtime has returned success for the operation
	PubTxStatusSucceeded PubTxStatus = "Succeeded"
	// PubTxStatusFailed happens when an error is reported by the infrastructure runtime
	PubTxStatusFailed PubTxStatus = "Failed"
	// BaseTxStatusFailed happens when the indexed transaction hash doesn't match any of the submitted hashes
	PubTxStatusConflict PubTxStatus = "Conflict"
	// PubTxStatusSuspended indicates we are not actively doing any work with this transaction right now, until it's resumed to pending again
	PubTxStatusSuspended PubTxStatus = "Suspended"
)

// TXUpdates specifies a set of updates that are possible on the base structure.
//
// Any non-nil fields will be set.
// Sub-objects are set as a whole, apart from TransactionHeaders where each field
// is considered and stored individually.
// JSONAny fields can be set explicitly to null using fftypes.NullString
//
// This is the update interface for the policy engine to update base status on the
// transaction object.
//
// There are separate setter functions for fields that depending on the persistence
// mechanism might be in separate tables - including History, Receipt, and Confirmations
type BaseTXUpdates struct {
	Status               *PubTxStatus         `json:"status"`
	From                 *string              `json:"from,omitempty"`
	To                   *string              `json:"to,omitempty"`
	Nonce                *ethtypes.HexInteger `json:"nonce,omitempty"`
	Value                *ethtypes.HexInteger `json:"value,omitempty"`
	GasPrice             *ethtypes.HexInteger `json:"gasPrice,omitempty"`
	MaxPriorityFeePerGas *ethtypes.HexInteger `json:"maxPriorityFeePerGas,omitempty"`
	MaxFeePerGas         *ethtypes.HexInteger `json:"maxFeePerGas,omitempty"`
	GasLimit             *ethtypes.HexInteger `json:"gas,omitempty"` // note this is required for some methods (eth_estimateGas)
	TransactionHash      *tktypes.Bytes32     `json:"transactionHash,omitempty"`
	FirstSubmit          *tktypes.Timestamp   `json:"firstSubmit,omitempty"`
	LastSubmit           *tktypes.Timestamp   `json:"lastSubmit,omitempty"`
	ErrorMessage         *string              `json:"errorMessage,omitempty"`
	SubmittedHashes      []string             `json:"submittedHashes,omitempty"`
}

type PublicTX struct {
	ID         uuid.UUID          `json:"id"`
	Created    *tktypes.Timestamp `json:"created"`
	Updated    *tktypes.Timestamp `json:"updated"`
	Status     PubTxStatus        `json:"status"`
	SubStatus  PubTxSubStatus     `json:"subStatus"`
	SequenceID string             `json:"sequenceId,omitempty"`
	*ethsigner.Transaction
	TransactionHash *tktypes.Bytes32   `json:"transactionHash,omitempty"`
	FirstSubmit     *tktypes.Timestamp `json:"firstSubmit,omitempty"`
	LastSubmit      *tktypes.Timestamp `json:"lastSubmit,omitempty"`
	ErrorMessage    string             `json:"errorMessage,omitempty"`
	// submitted transaction hashes are in a separate DB table, we load and manage it in memory in the same object for code convenience
	SubmittedHashes []string `json:"submittedHashes,omitempty"`
}

type PublicTransactionEvent struct {
	Type PublicTransactionEventType
	Tx   *PublicTX
}

// Handler checks received transaction process events and dispatch them to an event
// manager accordingly.
type PublicTxEventNotifier interface {
	Notify(ctx context.Context, e PublicTransactionEvent) error
}

type RequestOptions struct {
	ID       *uuid.UUID
	SignerID string
	GasLimit *ethtypes.HexInteger
}

// PubTxSubStatus is an intermediate status a transaction may go through
type PubTxSubStatus string

const (
	// PubTxSubStatusReceived indicates the transaction has been received by the connector
	PubTxSubStatusReceived PubTxSubStatus = "Received"
	// PubTxSubStatusStale indicates the transaction is now in stale
	PubTxSubStatusStale PubTxSubStatus = "Stale"
	// PubTxSubStatusTracking indicates we are tracking progress of the transaction
	PubTxSubStatusTracking PubTxSubStatus = "Tracking"
	// PubTxSubStatusConfirmed indicates we have confirmed that the transaction has been fully processed
	PubTxSubStatusConfirmed PubTxSubStatus = "Confirmed"
)

type BaseTxAction string

const (
	// BaseTxActionSign indicates the operation has been signed
	BaseTxActionSign BaseTxAction = "Sign"
)

const (
	// BaseTxActionStateTransition is a special value used for state transition entries, which are created using SetSubStatus
	BaseTxActionStateTransition BaseTxAction = "StateTransition"
	// BaseTxActionAssignNonce indicates that a nonce has been assigned to the transaction
	BaseTxActionAssignNonce BaseTxAction = "AssignNonce"
	// BaseTxActionRetrieveGasPrice indicates the operation is getting a gas price
	BaseTxActionRetrieveGasPrice BaseTxAction = "RetrieveGasPrice"
	// BaseTxActionSubmitTransaction indicates that the transaction has been submitted
	BaseTxActionSubmitTransaction BaseTxAction = "SubmitTransaction"
	// BaseTxActionConfirmTransaction indicates that the transaction has been confirmed
	BaseTxActionConfirmTransaction BaseTxAction = "Confirm"
)

type NextNonceCallback func(ctx context.Context, signer string) (uint64, error)

type PubTransactionQueries struct {
	NotIDAND   []string
	StatusOR   []string
	NotFromAND []string
	From       *string
	To         *string
	Sort       *string
	Limit      *int
	AfterNonce *big.Int
	HasValue   bool
}
type PublicTransactionStore interface {
	GetTransactionByID(ctx context.Context, txID string) (*PublicTX, error)
	InsertTransaction(ctx context.Context, tx *PublicTX) error
	UpdateTransaction(ctx context.Context, txID string, updates *BaseTXUpdates) error

	GetConfirmedTransaction(ctx context.Context, txID string) (iTX *blockindexer.IndexedTransaction, err error)

	AddSubStatusAction(ctx context.Context, txID string, subStatus PubTxSubStatus, action BaseTxAction, info *fftypes.JSONAny, err *fftypes.JSONAny, actionOccurred *fftypes.FFTime) error

	ListTransactions(ctx context.Context, filter *PubTransactionQueries) ([]*PublicTX, error)
}

type PreparedSubmission interface {
	ID() string
}

type PublicTxEngine interface {
	// Lifecycle functions

	// Init - setting a set of initialized toolkit plugins in the constructed transaction handler object. Safe checks & initialization
	//        can take place inside this function as well. It also enables toolkit plugins to be able to embed a reference to its parent
	//        transaction handler instance.
	Init(ctx context.Context, ethClient ethclient.EthClient, keymgr ethclient.KeyManager, txStore PublicTransactionStore, publicTXEventNotifier PublicTxEventNotifier, blockIndexer blockindexer.BlockIndexer)

	// Start - starting the transaction handler to handle inbound events.
	// It takes in a context, of which upon cancellation will stop the transaction handler.
	// It returns a read-only channel. When this channel gets closed, it indicates transaction handler has been stopped gracefully.
	// It returns an error when failed to start.
	Start(ctx context.Context) (done <-chan struct{}, err error)

	//Syncronous functions that are executed on the callers thread
	SubmitBatch(ctx context.Context, preparedSubmissions []PreparedSubmission) ([]*PublicTX, error)
	PrepareSubmissionBatch(ctx context.Context, reqOptions *RequestOptions, txPayloads []interface{}) (preparedSubmission []PreparedSubmission, submissionRejected bool, err error)

	// Event handling functions
	// Instructional events:
	// HandleNewTransaction - handles event of adding new transactions onto blockchain
	HandleNewTransaction(ctx context.Context, reqOptions *RequestOptions, txPayload interface{}) (mtx *PublicTX, submissionRejected bool, err error)
	// HandleSuspendTransaction - handles event of suspending a managed transaction
	HandleSuspendTransaction(ctx context.Context, txID string) (mtx *PublicTX, err error)
	// HandleResumeTransaction - handles event of resuming a suspended managed transaction
	HandleResumeTransaction(ctx context.Context, txID string) (mtx *PublicTX, err error)

	// Functions for auto-fueling
	GetPendingFuelingTransaction(ctx context.Context, sourceAddress string, destinationAddress string) (tx *PublicTX, err error)
	CheckTransactionCompleted(ctx context.Context, tx *PublicTX) (completed bool)
}

type PublicTxManager interface {
	ManagerLifecycle
	GetEngine() PublicTxEngine
}
