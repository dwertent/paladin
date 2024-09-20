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

package privatetxnmgr

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kaleido-io/paladin/core/internal/components"
	"github.com/kaleido-io/paladin/core/internal/statestore"
	"github.com/kaleido-io/paladin/core/mocks/componentmocks"
	"github.com/kaleido-io/paladin/core/pkg/blockindexer"
	"github.com/kaleido-io/paladin/core/pkg/ethclient"
	"github.com/kaleido-io/paladin/core/pkg/persistence"
	coreProto "github.com/kaleido-io/paladin/core/pkg/proto"
	pbEngine "github.com/kaleido-io/paladin/core/pkg/proto/engine"
	"github.com/kaleido-io/paladin/toolkit/pkg/algorithms"
	"github.com/kaleido-io/paladin/toolkit/pkg/log"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"gorm.io/gorm"
)

// Attempt to assert the behaviour of the private transaction manager as a whole component in isolation from the rest of the system
// Tests in this file do not mock anything else in this package or sub packages but does mock other components and managers in paladin as per their interfaces

func TestPrivateTxManagerInit(t *testing.T) {

	privateTxManager, mocks := NewPrivateTransactionMgrForTesting(t, tktypes.MustEthAddress(tktypes.RandHex(20)))
	err := privateTxManager.PostInit(mocks.allComponents)
	require.NoError(t, err)
}

func TestPrivateTxManagerInvalidTransaction(t *testing.T) {
	ctx := context.Background()

	privateTxManager, mocks := NewPrivateTransactionMgrForTesting(t, tktypes.MustEthAddress(tktypes.RandHex(20)))
	err := privateTxManager.PostInit(mocks.allComponents)
	require.NoError(t, err)

	err = privateTxManager.Start()
	require.NoError(t, err)

	txID, err := privateTxManager.HandleNewTx(ctx, &components.PrivateTransaction{})
	// no input domain should err
	assert.Regexp(t, "PD011800", err)
	assert.Empty(t, txID)
}

func TestPrivateTxManagerSimpleTransaction(t *testing.T) {
	//Submit a transaction that gets assembled with an attestation plan for a local endorser to sign the transaction
	ctx := context.Background()

	domainAddress := tktypes.MustEthAddress(tktypes.RandHex(20))
	privateTxManager, mocks := NewPrivateTransactionMgrForTesting(t, domainAddress)

	domainAddressString := domainAddress.String()

	initialised := make(chan struct{}, 1)
	mocks.domainSmartContract.On("InitTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.PreAssembly = &components.TransactionPreAssembly{
			RequiredVerifiers: []*prototk.ResolveVerifierRequest{
				{
					Lookup:    "alice",
					Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
				},
			},
		}
		initialised <- struct{}{}
	}).Return(nil)

	mocks.keyManager.On("ResolveKey", mock.Anything, "alice", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("aliceKeyHandle", "aliceVerifier", nil)
	// TODO check that the transaction is signed with this key

	assembled := make(chan struct{}, 1)
	mocks.domainSmartContract.On("AssembleTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)

		tx.PostAssembly = &components.TransactionPostAssembly{
			AssemblyResult: prototk.AssembleTransactionResponse_OK,
			InputStates: []*components.FullState{
				{
					ID:     tktypes.Bytes32(tktypes.RandBytes(32)),
					Schema: tktypes.Bytes32(tktypes.RandBytes(32)),
					Data:   tktypes.JSONString("foo"),
				},
			},
			AttestationPlan: []*prototk.AttestationRequest{
				{
					Name:            "notary",
					AttestationType: prototk.AttestationType_ENDORSE,
					Algorithm:       algorithms.ECDSA_SECP256K1_PLAINBYTES,
					Parties: []string{
						"domain1.contract1.notary",
					},
				},
			},
		}
		assembled <- struct{}{}

	}).Return(nil)

	mocks.keyManager.On("ResolveKey", mock.Anything, "domain1.contract1.notary", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("notaryKeyHandle", "notaryVerifier", nil)

	signingAddress := tktypes.RandHex(32)

	mocks.domainSmartContract.On("ResolveDispatch", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.Signer = signingAddress
	}).Return(nil)

	//TODO match endorsement request and verifier args
	mocks.domainSmartContract.On("EndorseTransaction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&components.EndorsementResult{
		Result:  prototk.EndorseTransactionResponse_SIGN,
		Payload: []byte("some-endorsement-bytes"),
		Endorser: &prototk.ResolvedVerifier{
			Lookup:    "notaryKeyHandle",
			Verifier:  "notaryVerifier",
			Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
		},
	}, nil)

	mocks.keyManager.On("Sign", mock.Anything, &coreProto.SignRequest{
		KeyHandle: "notaryKeyHandle",
		Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
		Payload:   []byte("some-endorsement-bytes"),
	}).Return(&coreProto.SignResponse{
		Payload: []byte("some-signature-bytes"),
	}, nil)

	mocks.domainSmartContract.On("PrepareTransaction", mock.Anything, mock.Anything).Return(nil)

	mockPreparedSubmission := componentmocks.NewPreparedSubmission(t)
	mockPreparedSubmissions := []components.PreparedSubmission{mockPreparedSubmission}

	mocks.publicTxEngine.On("PrepareSubmissionBatch", mock.Anything, mock.Anything, mock.Anything).Return(mockPreparedSubmissions, false, nil)

	publicTransactions := []*components.PublicTX{
		{
			ID: uuid.New(),
		},
	}
	mocks.publicTxEngine.On("SubmitBatch", mock.Anything, mock.Anything, mockPreparedSubmissions).Return(publicTransactions, nil)

	err := privateTxManager.Start()
	require.NoError(t, err)
	txID, err := privateTxManager.HandleNewTx(ctx, &components.PrivateTransaction{
		ID: uuid.New(),
		Inputs: &components.TransactionInputs{
			Domain: "domain1",
			To:     *domainAddress,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, txID)

	status := pollForStatus(ctx, t, "dispatched", privateTxManager, domainAddressString, txID, 2*time.Second)
	assert.Equal(t, "dispatched", status)
}

func TestPrivateTxManagerLocalEndorserSubmits(t *testing.T) {
}

func TestPrivateTxManagerRevertFromLocalEndorsement(t *testing.T) {
}

func TestPrivateTxManagerRemoteEndorser(t *testing.T) {
	ctx := context.Background()

	domainAddress := tktypes.MustEthAddress(tktypes.RandHex(20))
	privateTxManager, mocks := NewPrivateTransactionMgrForTesting(t, domainAddress)
	domainAddressString := domainAddress.String()

	remoteEngine, remoteEngineMocks := NewPrivateTransactionMgrForTesting(t, domainAddress)

	initialised := make(chan struct{}, 1)
	mocks.domainSmartContract.On("InitTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.PreAssembly = &components.TransactionPreAssembly{
			RequiredVerifiers: []*prototk.ResolveVerifierRequest{
				{
					Lookup:    "alice",
					Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
				},
			},
		}
		initialised <- struct{}{}
	}).Return(nil)

	mocks.keyManager.On("ResolveKey", mock.Anything, "alice", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("aliceKeyHandle", "aliceVerifier", nil)

	assembled := make(chan struct{}, 1)
	mocks.domainSmartContract.On("AssembleTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)

		tx.PostAssembly = &components.TransactionPostAssembly{
			AssemblyResult: prototk.AssembleTransactionResponse_OK,
			InputStates: []*components.FullState{
				{
					ID:     tktypes.Bytes32(tktypes.RandBytes(32)),
					Schema: tktypes.Bytes32(tktypes.RandBytes(32)),
					Data:   tktypes.JSONString("foo"),
				},
			},
			AttestationPlan: []*prototk.AttestationRequest{
				{
					Name:            "notary",
					AttestationType: prototk.AttestationType_ENDORSE,
					Algorithm:       algorithms.ECDSA_SECP256K1_PLAINBYTES,
					Parties: []string{
						"domain1.contract1.notary@othernode",
					},
				},
			},
		}
		assembled <- struct{}{}

	}).Return(nil)

	mocks.transportManager.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		go func() {
			transportMessage := args.Get(1).(*components.TransportMessage)
			remoteEngine.ReceiveTransportMessage(ctx, transportMessage)
		}()
	}).Return(nil).Maybe()

	remoteEngineMocks.transportManager.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		go func() {
			transportMessage := args.Get(1).(*components.TransportMessage)
			privateTxManager.ReceiveTransportMessage(ctx, transportMessage)
		}()
	}).Return(nil).Maybe()

	remoteEngineMocks.domainMgr.On("GetSmartContractByAddress", mock.Anything, *domainAddress).Return(remoteEngineMocks.domainSmartContract, nil)

	remoteEngineMocks.keyManager.On("ResolveKey", mock.Anything, "domain1.contract1.notary@othernode", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("notaryKeyHandle", "notaryVerifier", nil)

	signingAddress := tktypes.RandHex(32)

	mocks.domainSmartContract.On("ResolveDispatch", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.Signer = signingAddress
	}).Return(nil)

	//TODO match endorsement request and verifier args
	remoteEngineMocks.domainSmartContract.On("EndorseTransaction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&components.EndorsementResult{
		Result:  prototk.EndorseTransactionResponse_SIGN,
		Payload: []byte("some-endorsement-bytes"),
		Endorser: &prototk.ResolvedVerifier{
			Lookup:    "notaryKeyHandle",
			Verifier:  "notaryVerifier",
			Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
		},
	}, nil)
	remoteEngineMocks.keyManager.On("Sign", mock.Anything, &coreProto.SignRequest{
		KeyHandle: "notaryKeyHandle",
		Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
		Payload:   []byte("some-endorsement-bytes"),
	}).Return(&coreProto.SignResponse{
		Payload: []byte("some-signature-bytes"),
	}, nil)

	mocks.domainSmartContract.On("PrepareTransaction", mock.Anything, mock.Anything).Return(nil)

	mockPreparedSubmission := componentmocks.NewPreparedSubmission(t)
	mockPreparedSubmissions := []components.PreparedSubmission{mockPreparedSubmission}

	mocks.publicTxEngine.On("PrepareSubmissionBatch", mock.Anything, mock.Anything, mock.Anything).Return(mockPreparedSubmissions, false, nil)

	publicTransactions := []*components.PublicTX{
		{
			ID: uuid.New(),
		},
	}
	mocks.publicTxEngine.On("SubmitBatch", mock.Anything, mock.Anything, mockPreparedSubmissions).Return(publicTransactions, nil)

	err := privateTxManager.Start()
	assert.NoError(t, err)

	txID, err := privateTxManager.HandleNewTx(ctx, &components.PrivateTransaction{
		ID: uuid.New(),
		Inputs: &components.TransactionInputs{
			Domain: "domain1",
			To:     *domainAddress,
		},
	})
	assert.NoError(t, err)
	require.NotNil(t, txID)

	status := pollForStatus(ctx, t, "dispatched", privateTxManager, domainAddressString, txID, 200*time.Second)
	assert.Equal(t, "dispatched", status)

}

func TestPrivateTxManagerDependantTransactionEndorsedOutOfOrder(t *testing.T) {
	//2 transactions, one dependant on the other
	// we purposely endorse the first transaction late to ensure that the 2nd transaction
	// is still sequenced behind the first
	ctx := context.Background()

	domainAddress := tktypes.MustEthAddress(tktypes.RandHex(20))
	privateTxManager, mocks := NewPrivateTransactionMgrForTesting(t, domainAddress)

	domainAddressString := domainAddress.String()
	mocks.keyManager.On("ResolveKey", mock.Anything, "alice", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("aliceKeyHandle", "aliceVerifier", nil)

	mocks.domainSmartContract.On("InitTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.PreAssembly = &components.TransactionPreAssembly{
			RequiredVerifiers: []*prototk.ResolveVerifierRequest{
				{
					Lookup:    "alice",
					Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
				},
			},
		}
	}).Return(nil)

	// TODO check that the transaction is signed with this key

	states := []*components.FullState{
		{
			ID:     tktypes.Bytes32(tktypes.RandBytes(32)),
			Schema: tktypes.Bytes32(tktypes.RandBytes(32)),
			Data:   tktypes.JSONString("foo"),
		},
	}

	tx1 := &components.PrivateTransaction{
		ID: uuid.New(),
		Inputs: &components.TransactionInputs{
			Domain: "domain1",
			To:     *domainAddress,
			From:   "Alice",
		},
	}

	tx2 := &components.PrivateTransaction{
		ID: uuid.New(),
		Inputs: &components.TransactionInputs{
			Domain: "domain1",
			To:     *domainAddress,
			From:   "Bob",
		},
	}

	mocks.domainSmartContract.On("AssembleTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		switch tx.ID.String() {
		case tx1.ID.String():
			tx.PostAssembly = &components.TransactionPostAssembly{
				AssemblyResult: prototk.AssembleTransactionResponse_OK,
				OutputStates:   states,
				AttestationPlan: []*prototk.AttestationRequest{
					{
						Name:            "notary",
						AttestationType: prototk.AttestationType_ENDORSE,
						Algorithm:       algorithms.ECDSA_SECP256K1_PLAINBYTES,
						Parties: []string{
							"domain1.contract1.notary@othernode",
						},
					},
				},
			}
		case tx2.ID.String():
			tx.PostAssembly = &components.TransactionPostAssembly{
				AssemblyResult: prototk.AssembleTransactionResponse_OK,
				InputStates:    states,
				AttestationPlan: []*prototk.AttestationRequest{
					{
						Name:            "notary",
						AttestationType: prototk.AttestationType_ENDORSE,
						Algorithm:       algorithms.ECDSA_SECP256K1_PLAINBYTES,
						Parties: []string{
							"domain1.contract1.notary@othernode",
						},
					},
				},
			}
		default:
			assert.Fail(t, "Unexpected transaction ID")
		}
	}).Times(2).Return(nil)

	sentEndorsementRequest := make(chan struct{}, 1)
	mocks.transportManager.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		sentEndorsementRequest <- struct{}{}
	}).Return(nil).Maybe()

	mocks.domainSmartContract.On("PrepareTransaction", mock.Anything, mock.Anything).Return(nil)

	signingAddress := tktypes.RandHex(32)

	mocks.domainSmartContract.On("ResolveDispatch", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		tx := args.Get(1).(*components.PrivateTransaction)
		tx.Signer = signingAddress
	}).Return(nil)

	mockPreparedSubmission1 := componentmocks.NewPreparedSubmission(t)
	mockPreparedSubmission2 := componentmocks.NewPreparedSubmission(t)
	mockPreparedSubmissions := []components.PreparedSubmission{mockPreparedSubmission1, mockPreparedSubmission2}

	mocks.publicTxEngine.On("PrepareSubmissionBatch", mock.Anything, mock.Anything, mock.Anything).Return(mockPreparedSubmissions, false, nil)

	publicTransactions := []*components.PublicTX{
		{
			ID: uuid.New(),
		},
		{
			ID: uuid.New(),
		},
	}
	mocks.publicTxEngine.On("SubmitBatch", mock.Anything, mock.Anything, mockPreparedSubmissions).Return(publicTransactions, nil)

	err := privateTxManager.Start()
	require.NoError(t, err)

	tx1ID, err := privateTxManager.HandleNewTx(ctx, tx1)
	require.NoError(t, err)
	require.NotNil(t, tx1ID)

	tx2ID, err := privateTxManager.HandleNewTx(ctx, tx2)
	require.NoError(t, err)
	require.NotNil(t, tx2ID)

	// Neither transaction should be dispatched yet
	s, err := privateTxManager.GetTxStatus(ctx, domainAddressString, tx1ID)
	require.NoError(t, err)
	assert.NotEqual(t, "dispatch", s.Status)

	s, err = privateTxManager.GetTxStatus(ctx, domainAddressString, tx2ID)
	require.NoError(t, err)
	assert.NotEqual(t, "dispatch", s.Status)

	attestationResult := prototk.AttestationResult{
		Name:            "notary",
		AttestationType: prototk.AttestationType_ENDORSE,
		Payload:         tktypes.RandBytes(32),
	}

	attestationResultAny, err := anypb.New(&attestationResult)
	require.NoError(t, err)

	//wait for both transactions to send the endorsement request
	<-sentEndorsementRequest
	<-sentEndorsementRequest

	// endorse transaction 2 before 1 and check that 2 is not dispatched before 1
	endorsementResponse2 := &pbEngine.EndorsementResponse{
		ContractAddress: domainAddressString,
		TransactionId:   tx2ID,
		Endorsement:     attestationResultAny,
	}
	endorsementResponse2Bytes, err := proto.Marshal(endorsementResponse2)
	require.NoError(t, err)

	//now send the endorsement back
	privateTxManager.ReceiveTransportMessage(ctx, &components.TransportMessage{
		MessageType: "EndorsementResponse",
		Payload:     endorsementResponse2Bytes,
	})

	//unless the tests are running in short mode, wait a second to ensure that the transaction is not dispatched
	if !testing.Short() {
		time.Sleep(1 * time.Second)
	}
	s, err = privateTxManager.GetTxStatus(ctx, domainAddressString, tx1ID)
	require.NoError(t, err)
	assert.NotEqual(t, "dispatch", s.Status)

	s, err = privateTxManager.GetTxStatus(ctx, domainAddressString, tx2ID)
	require.NoError(t, err)
	assert.NotEqual(t, "dispatch", s.Status)

	// endorse transaction 1 and check that both it and 2 are dispatched
	endorsementResponse1 := &pbEngine.EndorsementResponse{
		ContractAddress: domainAddressString,
		TransactionId:   tx1ID,
		Endorsement:     attestationResultAny,
	}
	endorsementResponse1Bytes, err := proto.Marshal(endorsementResponse1)
	require.NoError(t, err)

	//now send the endorsement back
	privateTxManager.ReceiveTransportMessage(ctx, &components.TransportMessage{
		MessageType: "EndorsementResponse",
		Payload:     endorsementResponse1Bytes,
	})

	status := pollForStatus(ctx, t, "dispatched", privateTxManager, domainAddressString, tx1ID, 200*time.Second)
	assert.Equal(t, "dispatched", status)

	status = pollForStatus(ctx, t, "dispatched", privateTxManager, domainAddressString, tx2ID, 200*time.Second)
	assert.Equal(t, "dispatched", status)

	//TODO assert that transaction 1 got dispatched before 2

}

func TestPrivateTxManagerLocalBlockedTransaction(t *testing.T) {
	//TODO
	// 3 transactions, for different signing addresses, but two are is blocked by the other
	// when the earlier transaction is confirmed, both blocked transactions should be dispatched
}

func TestPrivateTxManagerMiniLoad(t *testing.T) {
	t.Skip("This test takes too long to be included by default.  It is still useful for local testing")
	//TODO this is actually quite a complex test given all the mocking.  Maybe this should be converted to a wider component test
	// where the real publicTxEngine is used rather than a mock
	r := rand.New(rand.NewSource(42))
	loadTests := []struct {
		name            string
		latency         func() time.Duration
		numTransactions int
	}{
		{"no-latency", func() time.Duration { return 0 }, 5},
		{"low-latency", func() time.Duration { return 10 * time.Millisecond }, 500},
		{"medium-latency", func() time.Duration { return 50 * time.Millisecond }, 500},
		{"high-latency", func() time.Duration { return 100 * time.Millisecond }, 500},
		{"random-none-to-low-latency", func() time.Duration { return time.Duration(r.Intn(10)) * time.Millisecond }, 500},
		{"random-none-to-high-latency", func() time.Duration { return time.Duration(r.Intn(100)) * time.Millisecond }, 500},
	}
	//500 is the maximum we can do in this test for now until either
	//a) implement config to allow us to define MaxConcurrentTransactions
	//b) implement ( or mock) transaction dispatch processing all the way to confirmation

	for _, test := range loadTests {
		t.Run(test.name, func(t *testing.T) {

			ctx := context.Background()

			domainAddress := tktypes.MustEthAddress(tktypes.RandHex(20))
			privateTxManager, mocks := NewPrivateTransactionMgrForTestingWithFakePublicTxEngine(t, domainAddress, newFakePublicTxEngine(t))

			remoteEngine, remoteEngineMocks := NewPrivateTransactionMgrForTestingWithFakePublicTxEngine(t, domainAddress, newFakePublicTxEngine(t))

			dependenciesByTransactionID := make(map[string][]string) // populated during assembly stage
			nonceByTransactionID := make(map[string]uint64)          // populated when dispatch event recieved and used later to check that the nonce order matchs the dependency order

			unclaimedPendingStatesToMintingTransaction := make(map[tktypes.Bytes32]string)

			mocks.domainSmartContract.On("InitTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				tx := args.Get(1).(*components.PrivateTransaction)
				tx.PreAssembly = &components.TransactionPreAssembly{
					RequiredVerifiers: []*prototk.ResolveVerifierRequest{
						{
							Lookup:    "alice",
							Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
						},
					},
				}
			}).Return(nil)
			mocks.keyManager.On("ResolveKey", mock.Anything, "alice", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("aliceKeyHandle", "aliceVerifier", nil)

			failEarly := make(chan string, 1)

			assembleConcurrency := 0
			mocks.domainSmartContract.On("AssembleTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				//assert that we are not assembling more than 1 transaction at a time
				if assembleConcurrency > 0 {
					failEarly <- "Assembling more than one transaction at a time"
				}
				require.Equal(t, assembleConcurrency, 0, "Assembling more than one transaction at a time")

				assembleConcurrency++
				defer func() { assembleConcurrency-- }()

				// chose a number of dependencies at random 0, 1, 2, 3
				// for each dependency, chose a different unclaimed pending state to spend
				tx := args.Get(1).(*components.PrivateTransaction)

				var inputStates []*components.FullState
				numDependencies := min(r.Intn(4), len(unclaimedPendingStatesToMintingTransaction))
				dependencies := make([]string, numDependencies)
				for i := 0; i < numDependencies; i++ {
					// chose a random unclaimed pending state to spend
					stateIndex := r.Intn(len(unclaimedPendingStatesToMintingTransaction))

					keys := make([]tktypes.Bytes32, len(unclaimedPendingStatesToMintingTransaction))
					keyIndex := 0
					for keyName := range unclaimedPendingStatesToMintingTransaction {

						keys[keyIndex] = keyName
						keyIndex++
					}
					stateID := keys[stateIndex]
					inputStates = append(inputStates, &components.FullState{
						ID: stateID,
					})

					log.L(ctx).Infof("input state %s, numDependencies %d i %d", stateID, numDependencies, i)
					dependencies[i] = unclaimedPendingStatesToMintingTransaction[stateID]
					delete(unclaimedPendingStatesToMintingTransaction, stateID)
				}
				dependenciesByTransactionID[tx.ID.String()] = dependencies

				numOutputStates := r.Intn(4)
				outputStates := make([]*components.FullState, numOutputStates)
				for i := 0; i < numOutputStates; i++ {
					stateID := tktypes.Bytes32(tktypes.RandBytes(32))
					outputStates[i] = &components.FullState{
						ID: stateID,
					}
					unclaimedPendingStatesToMintingTransaction[stateID] = tx.ID.String()
				}

				tx.PostAssembly = &components.TransactionPostAssembly{

					AssemblyResult: prototk.AssembleTransactionResponse_OK,
					OutputStates:   outputStates,
					InputStates:    inputStates,
					AttestationPlan: []*prototk.AttestationRequest{
						{
							Name:            "notary",
							AttestationType: prototk.AttestationType_ENDORSE,
							Algorithm:       algorithms.ECDSA_SECP256K1_PLAINBYTES,
							Parties: []string{
								"domain1.contract1.notary@othernode",
							},
						},
					},
				}
			}).Return(nil)

			mocks.transportManager.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				go func() {
					//inject random latency on the network
					time.Sleep(test.latency())
					transportMessage := args.Get(1).(*components.TransportMessage)
					remoteEngine.ReceiveTransportMessage(ctx, transportMessage)
				}()
			}).Return(nil).Maybe()

			remoteEngineMocks.transportManager.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				go func() {
					//inject random latency on the network
					time.Sleep(test.latency())
					transportMessage := args.Get(1).(*components.TransportMessage)
					privateTxManager.ReceiveTransportMessage(ctx, transportMessage)
				}()
			}).Return(nil).Maybe()
			remoteEngineMocks.domainMgr.On("GetSmartContractByAddress", mock.Anything, *domainAddress).Return(remoteEngineMocks.domainSmartContract, nil)

			remoteEngineMocks.keyManager.On("ResolveKey", mock.Anything, "domain1.contract1.notary@othernode", algorithms.ECDSA_SECP256K1_PLAINBYTES).Return("notaryKeyHandle", "notaryVerifier", nil)

			signingAddress := tktypes.RandHex(32)

			mocks.domainSmartContract.On("ResolveDispatch", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				tx := args.Get(1).(*components.PrivateTransaction)
				tx.Signer = signingAddress
			}).Return(nil)

			//TODO match endorsement request and verifier args
			remoteEngineMocks.domainSmartContract.On("EndorseTransaction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&components.EndorsementResult{
				Result:  prototk.EndorseTransactionResponse_SIGN,
				Payload: []byte("some-endorsement-bytes"),
				Endorser: &prototk.ResolvedVerifier{
					Lookup:    "notaryKeyHandle",
					Verifier:  "notaryVerifier",
					Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
				},
			}, nil)
			remoteEngineMocks.keyManager.On("Sign", mock.Anything, &coreProto.SignRequest{
				KeyHandle: "notaryKeyHandle",
				Algorithm: algorithms.ECDSA_SECP256K1_PLAINBYTES,
				Payload:   []byte("some-endorsement-bytes"),
			}).Return(&coreProto.SignResponse{
				Payload: []byte("some-signature-bytes"),
			}, nil)

			mocks.domainSmartContract.On("PrepareTransaction", mock.Anything, mock.Anything).Return(nil)

			expectedNonce := uint64(0)

			numDispatched := 0
			allDispatched := make(chan bool, 1)
			nonceWriterLock := sync.Mutex{}
			privateTxManager.Subscribe(ctx, func(event components.PrivateTxEvent) {
				nonceWriterLock.Lock()
				defer nonceWriterLock.Unlock()
				numDispatched++
				switch event := event.(type) {
				case *components.TransactionDispatchedEvent:
					assert.Equal(t, expectedNonce, event.Nonce)
					expectedNonce++
					nonceByTransactionID[event.TransactionID] = event.Nonce
				}
				if numDispatched == test.numTransactions {
					allDispatched <- true
				}
			})

			err := privateTxManager.Start()
			require.NoError(t, err)

			for i := 0; i < test.numTransactions; i++ {
				tx := &components.PrivateTransaction{
					ID: uuid.New(),
					Inputs: &components.TransactionInputs{
						Domain: "domain1",
						To:     *domainAddress,
						From:   "Alice",
					},
				}
				txID, err := privateTxManager.HandleNewTx(ctx, tx)
				require.NoError(t, err)
				require.NotNil(t, txID)
			}

			haveAllDispatched := false
		out:
			for {
				select {
				case <-time.After(timeTillDeadline(t)):
					log.L(ctx).Errorf("Timed out waiting for all transactions to be dispatched")
					assert.Fail(t, "Timed out waiting for all transactions to be dispatched")
					break out
				case <-allDispatched:
					haveAllDispatched = true
					break out
				case reason := <-failEarly:
					require.Fail(t, reason)
				}
			}

			if haveAllDispatched {
				//check that they were dispatched a valid order ( i.e. no transaction was dispatched before its dependencies)
				for txId, nonce := range nonceByTransactionID {
					dependencies := dependenciesByTransactionID[txId]
					for _, depTxID := range dependencies {
						depNonce, ok := nonceByTransactionID[depTxID]
						assert.True(t, ok)
						assert.True(t, depNonce < nonce, "Transaction %s (nonce %d) was dispatched before its dependency %s (nonce %d)", txId, nonce, depTxID, depNonce)
					}
				}
			}
		})
	}
}

func pollForStatus(ctx context.Context, t *testing.T, expectedStatus string, privateTxManager components.PrivateTxManager, domainAddressString, txID string, duration time.Duration) string {
	timeout := time.After(duration)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeout:
			// Timeout reached, exit the loop
			assert.Failf(t, "Timed out waiting for status %s", expectedStatus)
			s, err := privateTxManager.GetTxStatus(ctx, domainAddressString, txID)
			require.NoError(t, err)
			return s.Status
		case <-tick:
			s, err := privateTxManager.GetTxStatus(ctx, domainAddressString, txID)
			if s.Status == expectedStatus {
				return s.Status
			}
			require.NoError(t, err)
		}
	}
}

type dependencyMocks struct {
	allComponents        *componentmocks.AllComponents
	domainStateInterface *componentmocks.DomainStateInterface
	domainSmartContract  *componentmocks.DomainSmartContract
	domainMgr            *componentmocks.DomainManager
	transportManager     *componentmocks.TransportManager
	stateStore           *componentmocks.StateStore
	keyManager           *componentmocks.KeyManager
	publicTxManager      *componentmocks.PublicTxManager
	publicTxEngine       *componentmocks.PublicTxEngine
}

// For Black box testing we return components.PrivateTxManager
func NewPrivateTransactionMgrForTesting(t *testing.T, domainAddress *tktypes.EthAddress) (components.PrivateTxManager, *dependencyMocks) {
	// by default create a mock publicTxEngine if no fake was provided
	fakePublicTxEngine := componentmocks.NewPublicTxEngine(t)
	privateTxManager, mocks := NewPrivateTransactionMgrForTestingWithFakePublicTxEngine(t, domainAddress, fakePublicTxEngine)
	mocks.publicTxEngine = fakePublicTxEngine
	return privateTxManager, mocks
}

type fakePublicTxEngine struct {
	t *testing.T
}

// CheckTransactionCompleted implements components.PublicTxEngine.
func (f *fakePublicTxEngine) CheckTransactionCompleted(ctx context.Context, tx *components.PublicTX) (completed bool) {
	panic("unimplemented")
}

// GetPendingFuelingTransaction implements components.PublicTxEngine.
func (f *fakePublicTxEngine) GetPendingFuelingTransaction(ctx context.Context, sourceAddress string, destinationAddress string) (tx *components.PublicTX, err error) {
	panic("unimplemented")
}

// HandleNewTransaction implements components.PublicTxEngine.
func (f *fakePublicTxEngine) HandleNewTransaction(ctx context.Context, reqOptions *components.RequestOptions, txPayload interface{}) (mtx *components.PublicTX, submissionRejected bool, err error) {
	panic("unimplemented")
}

// HandleResumeTransaction implements components.PublicTxEngine.
func (f *fakePublicTxEngine) HandleResumeTransaction(ctx context.Context, txID string) (mtx *components.PublicTX, err error) {
	panic("unimplemented")
}

// HandleSuspendTransaction implements components.PublicTxEngine.
func (f *fakePublicTxEngine) HandleSuspendTransaction(ctx context.Context, txID string) (mtx *components.PublicTX, err error) {
	panic("unimplemented")
}

// Init implements components.PublicTxEngine.
func (f *fakePublicTxEngine) Init(ctx context.Context, ethClient ethclient.EthClient, keymgr ethclient.KeyManager, txStore components.PublicTransactionStore, publicTXEventNotifier components.PublicTxEventNotifier, blockIndexer blockindexer.BlockIndexer) {
	panic("unimplemented")
}

// Start implements components.PublicTxEngine.
func (f *fakePublicTxEngine) Start(ctx context.Context) (done <-chan struct{}, err error) {
	panic("unimplemented")
}

//for this test, we need a hand written fake rather than a simple mock for publicTxEngine

// PrepareSubmissionBatch implements components.PublicTxEngine.
func (f *fakePublicTxEngine) PrepareSubmissionBatch(ctx context.Context, reqOptions *components.RequestOptions, txPayloads []interface{}) (preparedSubmission []components.PreparedSubmission, submissionRejected bool, err error) {
	mockPreparedSubmissions := make([]components.PreparedSubmission, 0, len(txPayloads))
	for range txPayloads {
		mockPreparedSubmissions = append(mockPreparedSubmissions, componentmocks.NewPreparedSubmission(f.t))
	}
	return mockPreparedSubmissions, false, nil
}

// SubmitBatch implements components.PublicTxEngine.
func (f *fakePublicTxEngine) SubmitBatch(ctx context.Context, tx *gorm.DB, preparedSubmissions []components.PreparedSubmission) ([]*components.PublicTX, error) {
	publicTransactions := make([]*components.PublicTX, 0, len(preparedSubmissions))

	for range preparedSubmissions {
		publicTransactions = append(publicTransactions, &components.PublicTX{
			ID: uuid.New(),
		})
	}
	return publicTransactions, nil
}

func newFakePublicTxEngine(t *testing.T) components.PublicTxEngine {
	return &fakePublicTxEngine{
		t: t,
	}
}

func NewPrivateTransactionMgrForTestingWithFakePublicTxEngine(t *testing.T, domainAddress *tktypes.EthAddress, fakePublicTxEngine components.PublicTxEngine) (components.PrivateTxManager, *dependencyMocks) {

	ctx := context.Background()
	mocks := &dependencyMocks{
		allComponents:        componentmocks.NewAllComponents(t),
		domainStateInterface: componentmocks.NewDomainStateInterface(t),
		domainSmartContract:  componentmocks.NewDomainSmartContract(t),
		domainMgr:            componentmocks.NewDomainManager(t),
		transportManager:     componentmocks.NewTransportManager(t),
		stateStore:           componentmocks.NewStateStore(t),
		keyManager:           componentmocks.NewKeyManager(t),
		publicTxManager:      componentmocks.NewPublicTxManager(t),
	}
	mocks.allComponents.On("StateStore").Return(mocks.stateStore).Maybe()
	mocks.allComponents.On("DomainManager").Return(mocks.domainMgr).Maybe()
	mocks.allComponents.On("TransportManager").Return(mocks.transportManager).Maybe()
	mocks.allComponents.On("KeyManager").Return(mocks.keyManager).Maybe()
	mocks.allComponents.On("PublicTxManager").Return(mocks.publicTxManager).Maybe()
	mocks.publicTxManager.On("GetEngine").Return(fakePublicTxEngine).Maybe()
	mocks.domainMgr.On("GetSmartContractByAddress", mock.Anything, *domainAddress).Maybe().Return(mocks.domainSmartContract, nil)
	mocks.allComponents.On("Persistence").Return(persistence.NewUnitTestPersistence(ctx)).Maybe()

	mocks.stateStore.On("RunInDomainContext", mock.Anything, mock.AnythingOfType("statestore.DomainContextFunction")).Run(func(args mock.Arguments) {
		fn := args.Get(1).(statestore.DomainContextFunction)
		err := fn(context.Background(), mocks.domainStateInterface)
		assert.NoError(t, err)
	}).Maybe().Return(nil)

	e := NewPrivateTransactionMgr(ctx, tktypes.RandHex(16), &Config{})
	err := e.PostInit(mocks.allComponents)
	assert.NoError(t, err)
	return e, mocks

}

func timeTillDeadline(t *testing.T) time.Duration {
	deadline, ok := t.Deadline()
	if !ok {
		//there was no -timeout flag, default to 10 seconds
		deadline = time.Now().Add(10 * time.Second)
	}
	timeRemaining := time.Until(deadline)
	//Need to leave some time to ensure that polling assertions fail before the test itself timesout
	//otherwise we don't see diagnostic info for things like GoExit called by mocks etc
	if timeRemaining < 100*time.Millisecond {
		return 0
	}
	return timeRemaining - 100*time.Millisecond
}
