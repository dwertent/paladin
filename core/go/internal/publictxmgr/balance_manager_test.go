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

package publictxmgr

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/kaleido-io/paladin/config/pkg/confutil"
	"github.com/kaleido-io/paladin/config/pkg/pldconf"
	"github.com/kaleido-io/paladin/core/mocks/componentmocks"

	"github.com/kaleido-io/paladin/core/pkg/ethclient"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestBalanceManager(t *testing.T, autoFuel bool, cbs ...func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig)) (context.Context, *BalanceManagerWithInMemoryTracking, *pubTxManager, *mocksAndTestControl, func()) {
	ctx, ble, m, done := newTestPublicTxManager(t, false, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		if autoFuel {
			conf.BalanceManager.AutoFueling.Source = confutil.P("autofueler")

			autoFuelSourceAddr := tktypes.RandAddress()

			keyMapping := &pldapi.KeyMappingAndVerifier{
				KeyMappingWithPath: &pldapi.KeyMappingWithPath{
					KeyMapping: &pldapi.KeyMapping{
						Identifier: "autofueler",
					},
				},
				Verifier: &pldapi.KeyVerifier{
					Verifier: autoFuelSourceAddr.String(),
				},
			}
			mockKeyMgr := m.keyManager.(*componentmocks.KeyManager)
			mockKeyMgr.On("ResolveKeyNewDatabaseTX", mock.Anything, "autofueler", mock.Anything, mock.Anything).
				Return(keyMapping, nil).Maybe()
		}
		for _, cb := range cbs {
			cb(m, conf)
		}
	})

	balanceManager, err := NewBalanceManagerWithInMemoryTracking(ctx, ble.conf, ble)
	require.NoError(t, err)
	return ctx, balanceManager.(*BalanceManagerWithInMemoryTracking), ble, m, done
}

func TestNewBalanceManagerError(t *testing.T) {
	ctx, ble, _, done := newTestPublicTxManager(t, false)
	defer done()

	ble.conf.BalanceManager.AutoFueling.MaxDestBalance = confutil.P("2")
	ble.conf.BalanceManager.AutoFueling.MinDestBalance = confutil.P("3")
	_, err := NewBalanceManagerWithInMemoryTracking(ctx, ble.conf, ble)
	assert.Regexp(t, "PD011903", err.Error())

	ble.conf.BalanceManager.AutoFueling.MaxDestBalance = confutil.P("4")
	ble.conf.BalanceManager.AutoFueling.MinThreshold = confutil.P("10")

	_, err = NewBalanceManagerWithInMemoryTracking(ctx, ble.conf, ble)
	assert.Error(t, err)
	assert.Regexp(t, "PD011904", err.Error())
}

func TestIsAutoFuelingEnabled(t *testing.T) {
	ctx, bm, _, _, done := newTestBalanceManager(t, false)
	assert.False(t, bm.IsAutoFuelingEnabled(ctx))
	done()

	ctx, bm, _, _, done = newTestBalanceManager(t, true)
	assert.True(t, bm.IsAutoFuelingEnabled(ctx))
	done()
}

func TestNotifyAddressBalanceChanged(t *testing.T) {
	ctx, bm, _, _, done := newTestBalanceManager(t, false)
	defer done()

	exampleAddr := *tktypes.RandAddress()
	assert.Equal(t, false, bm.addressBalanceChangedMap[exampleAddr])
	bm.NotifyAddressBalanceChanged(ctx, exampleAddr)
	assert.Equal(t, true, bm.addressBalanceChangedMap[exampleAddr])
}

func TestGetAddressBalance(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, false)
	defer done()

	const balanceOld = uint64(400)
	const balanceNew = uint64(500)

	exampleAddr := *tktypes.RandAddress()

	m.ethClient.On("GetBalance", mock.Anything, exampleAddr, "latest").Return(tktypes.Uint64ToUint256(balanceOld), nil).Once()

	m.ethClient.On("GetBalance", mock.Anything, exampleAddr, "latest").Return(tktypes.Uint64ToUint256(balanceNew), nil).Once()

	m.ethClient.On("GetBalance", mock.Anything, exampleAddr, "latest").Return(nil, errors.New("pop")).Once()

	addressAccount, err := bm.GetAddressBalance(ctx, exampleAddr)
	require.NoError(t, err)
	assert.NotNil(t, addressAccount)
	assert.Equal(t, balanceOld, addressAccount.Balance.Uint64())

	// next get should use the cache
	addressAccount, err = bm.GetAddressBalance(ctx, exampleAddr)
	require.NoError(t, err)
	assert.Equal(t, balanceOld, addressAccount.Balance.Uint64())

	// next get should retrieve the balance again
	bm.NotifyAddressBalanceChanged(ctx, exampleAddr)
	addressAccount, err = bm.GetAddressBalance(ctx, exampleAddr)
	require.NoError(t, err)
	assert.Equal(t, balanceNew, addressAccount.Balance.Uint64())

	// test error
	bm.NotifyAddressBalanceChanged(ctx, exampleAddr)
	_, err = bm.GetAddressBalance(ctx, exampleAddr)
	assert.Error(t, err)
}

func TestAddressAccountSpend(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true)
	defer done()

	exampleAddr := *tktypes.RandAddress()

	const balanceOld = uint64(400)

	m.ethClient.On("GetBalance", mock.Anything, exampleAddr, "latest").Return(tktypes.Uint64ToUint256(balanceOld), nil).Once()
	addressAccount, err := bm.GetAddressBalance(ctx, exampleAddr)
	require.NoError(t, err)
	assert.NotNil(t, addressAccount)
	assert.Equal(t, balanceOld, addressAccount.Balance.Uint64())
	assert.Equal(t, "0", addressAccount.Spent.String())
	assert.Equal(t, "0", addressAccount.MinCost.String())
	assert.Equal(t, "0", addressAccount.MaxCost.String())
	assert.Equal(t, 0, addressAccount.SpentTransactionCount)

	initialBalance := big.NewInt(int64(balanceOld))
	spent1 := big.NewInt(10)

	ats := addressAccount.Spend(ctx, spent1)
	expectedNewAvailableToSpend := initialBalance.Sub(initialBalance, spent1)
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent1.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent1.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 1, addressAccount.SpentTransactionCount)

	spent2 := big.NewInt(20)
	ats = addressAccount.Spend(ctx, spent2)
	expectedNewAvailableToSpend = expectedNewAvailableToSpend.Sub(expectedNewAvailableToSpend, spent2)
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent1.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent2.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 2, addressAccount.SpentTransactionCount)

	// negative number gets ignored
	spent3 := big.NewInt(-30)
	ats = addressAccount.Spend(ctx, spent3)
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent1.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent2.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 2, addressAccount.SpentTransactionCount)

	// set new min cost
	spent4 := big.NewInt(5)
	ats = addressAccount.Spend(ctx, spent4)
	expectedNewAvailableToSpend = expectedNewAvailableToSpend.Sub(expectedNewAvailableToSpend, spent4)
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent4.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent2.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 3, addressAccount.SpentTransactionCount)

	// 0 gets ignored
	ats = addressAccount.Spend(ctx, big.NewInt(0))
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent4.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent2.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 3, addressAccount.SpentTransactionCount)

	balanceCopy := new(big.Int)
	balanceCopy.Set(addressAccount.Balance)
	amountToSpendAllBalance := balanceCopy.Sub(balanceCopy, addressAccount.Spent)
	spent5 := amountToSpendAllBalance.Add(amountToSpendAllBalance, big.NewInt(1))
	expectedNewAvailableToSpend = expectedNewAvailableToSpend.Sub(expectedNewAvailableToSpend, spent5)
	ats = addressAccount.Spend(ctx, spent5)
	assert.Equal(t, -1, ats.Sign())
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent4.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent5.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 4, addressAccount.SpentTransactionCount)

	// do spent5 again
	expectedNewAvailableToSpend = expectedNewAvailableToSpend.Sub(expectedNewAvailableToSpend, spent5)
	ats = addressAccount.Spend(ctx, spent5)
	assert.Equal(t, -1, ats.Sign())
	assert.Equal(t, expectedNewAvailableToSpend.String(), ats.String())
	assert.Equal(t, spent4.String(), addressAccount.MinCost.String())
	assert.Equal(t, spent5.String(), addressAccount.MaxCost.String())
	assert.Equal(t, 5, addressAccount.SpentTransactionCount)
}

func TestTopUpAddressNoOpScenarios(t *testing.T) {
	ctx, bm, _, _, done := newTestBalanceManager(t, true)
	defer done()

	// no spent on the account
	fuelingTx, err := bm.TopUpAccount(ctx, &AddressAccount{
		Spent: big.NewInt(0),
	})
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)

	// enough balance
	fuelingTx, err = bm.TopUpAccount(ctx, &AddressAccount{
		Spent:   big.NewInt(10),
		Balance: big.NewInt(10),
	})
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)

	// destination address already reached max amount
	bm.maxDestBalance = big.NewInt(100)
	fuelingTx, err = bm.TopUpAccount(ctx, &AddressAccount{
		Spent:                 big.NewInt(10),
		Balance:               big.NewInt(100),
		SpentTransactionCount: 10,
	})
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)

	// no source address configured
	bm.sourceAddress = nil
	fuelingTx, err = bm.TopUpAccount(ctx, &AddressAccount{
		Spent:                 big.NewInt(10),
		Balance:               big.NewInt(0),
		SpentTransactionCount: 10,
	})
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)

}

func expectFuelingEqual(t *testing.T, fuelingTx *pldapi.PublicTx, amountToTransfer uint64, from, to tktypes.EthAddress) {
	assert.Equal(t, from, fuelingTx.From)
	assert.Equal(t, to, *fuelingTx.To)
	assert.Equal(t, *tktypes.Uint64ToUint256(amountToTransfer), *fuelingTx.Value)
}

func mockAutoFuelTransactionSubmit(m *mocksAndTestControl, bm *BalanceManagerWithInMemoryTracking, uncachedBalance bool) {
	// Then insert of the auto-fueling transaction
	m.db.ExpectBegin()
	m.db.ExpectQuery("INSERT.*public_txns").WillReturnRows(m.db.NewRows([]string{"pub_txn_id"}).AddRow(12345))
	m.db.ExpectCommit()

	if uncachedBalance {
		// Mock the sufficient balance on the auto-fueling source address, and the nonce assignment
		m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(400), nil).Once()
	}

	// Gas estimate for the auto-fueling TX
	m.ethClient.On("EstimateGasNoResolve", mock.Anything, mock.Anything, mock.Anything).
		Return(ethclient.EstimateGasResult{GasLimit: tktypes.HexUint64(10)}, nil).Once()
}

func TestTopUpWithNoAmountModificationWithMultipleFuelingTxs(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}

	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	expectedTopUpAmount := big.NewInt(100)
	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)

	// Test no new fueling transaction when the current one is pending
	accountToTopUp2 := &AddressAccount{
		Balance:               big.NewInt(200),
		Spent:                 big.NewInt(250),
		Address:               testDestAddress,
		SpentTransactionCount: 1,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(50),
	}

	// return not yet completed, so should return the existing pending transaction
	m.db.ExpectQuery("SELECT.*public_txns").
		WillReturnRows(sqlmock.NewRows([]string{"from"}).AddRow(
			*bm.sourceAddress,
		))

	newFuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp2)
	require.NoError(t, err)
	expectFuelingEqual(t, newFuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)

	// current transaction completed, replace with new transaction
	expectedTopUpAmount2 := big.NewInt(50)
	m.db.ExpectQuery("SELECT.*public_txns").WillReturnRows(sqlmock.NewRows([]string{"from", `Completed__tx_hash`}).
		AddRow(*bm.sourceAddress, tktypes.RandBytes32()))

	mockAutoFuelTransactionSubmit(m, bm, false)

	fuelingTx2, err := bm.TopUpAccount(ctx, accountToTopUp2)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx2, expectedTopUpAmount2.Uint64(), *bm.sourceAddress, testDestAddress)

	// test when couldn't record the result of the submitted transaction
	// also do a balance look up
	accountToTopUp3 := &AddressAccount{
		Balance:               big.NewInt(250),
		Spent:                 big.NewInt(300),
		Address:               testDestAddress,
		SpentTransactionCount: 1,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(50),
	}
	expectedTopUpAmount3 := big.NewInt(50)
	bm.NotifyAddressBalanceChanged(ctx, *bm.sourceAddress)
	m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(50), nil).Once()

	m.db.ExpectQuery("SELECT.*public_txns").WillReturnRows(sqlmock.NewRows([]string{"from", `Completed__tx_hash`}).
		AddRow(*bm.sourceAddress, tktypes.RandBytes32()))
	m.db.ExpectBegin()

	m.ethClient.On("EstimateGasNoResolve", mock.Anything, mock.Anything, mock.Anything).
		Return(ethclient.EstimateGasResult{}, fmt.Errorf("pop")).Once()

	newFuelingTx, err = bm.TopUpAccount(ctx, accountToTopUp3)
	assert.Error(t, err)
	assert.Equal(t, "pop", err.Error())
	assert.Nil(t, newFuelingTx)

	// test that we can recover if the transaction was actually registered in DB
	// also do a address balance re-lookup
	m.db.ExpectQuery("SELECT.*public_txns").
		WillReturnRows(sqlmock.NewRows([]string{"from", "to", "value"}).AddRow(
			*bm.sourceAddress, testDestAddress, (*tktypes.HexUint256)(expectedTopUpAmount3),
		))
	m.db.ExpectQuery("SELECT.*public_txns").WillReturnRows(sqlmock.NewRows([]string{"from", "to", "value", `Completed__tx_hash`}).
		AddRow(*bm.sourceAddress, testDestAddress, (*tktypes.HexUint256)(expectedTopUpAmount3), nil /* incomplete */))
	fuelingTx3, err := bm.TopUpAccount(ctx, accountToTopUp3)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx3, expectedTopUpAmount3.Uint64(), *bm.sourceAddress, testDestAddress)
}

func TestTopUpSuccessTopUpMinAheadUseMin(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	// set the minimum to have 2 extra spaces
	bm.proactiveFuelingTransactionTotal = 4
	bm.proactiveFuelingCalcMethod = pldconf.ProactiveAutoFuelingCalcMethodMin

	// the expectTopUpAmount should include min Value (50) multiply 2 extra space we set
	expectedTopUpAmount := big.NewInt(200)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)

}

func TestTopUpSuccessTopUpMinAheadUseMax(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	// set the minimum to have 2 extra spaces
	bm.proactiveFuelingTransactionTotal = 4
	bm.proactiveFuelingCalcMethod = pldconf.ProactiveAutoFuelingCalcMethodMax

	// the expectTopUpAmount should include max Value (150) multiply 2 extra space we set
	expectedTopUpAmount := big.NewInt(400)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)

}

func TestTopUpSuccessTopUpMinAheadUseAvg(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	// set the minimum to have 2 extra spaces
	bm.proactiveFuelingTransactionTotal = 4
	bm.proactiveFuelingCalcMethod = pldconf.ProactiveAutoFuelingCalcMethodAverage

	// the expectTopUpAmount should include avg Value (100) multiply 2 extra space we set
	expectedTopUpAmount := big.NewInt(300)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)

}

func TestTopUpSuccessUseMinDestBalance(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	// set min top up to balance to 250 (50 above the required amount)
	bm.minDestBalance = big.NewInt(250)
	expectedTopUpAmount := big.NewInt(150)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)
}

func TestTopUpSuccessUseMaxDestBalance(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	mockAutoFuelTransactionSubmit(m, bm, true)

	// set max top up to balance to 150 (50 below the required amount)
	bm.maxDestBalance = big.NewInt(150)
	expectedTopUpAmount := big.NewInt(50)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	expectFuelingEqual(t, fuelingTx, expectedTopUpAmount.Uint64(), *bm.sourceAddress, testDestAddress)
}

func TestTopUpNoOpAlreadyAboveMaxDestBalance(t *testing.T) {
	ctx, bm, _, _, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}

	// set max top up to balance to 90, which is below the balance
	bm.maxDestBalance = big.NewInt(90)
	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)
}

func TestTopUpNoOpAmountBelowMinThreshold(t *testing.T) {
	ctx, bm, _, _, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}

	// set minimum top up threshold to 150, which is above the required amount
	bm.minThreshold = big.NewInt(150)
	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	require.NoError(t, err)
	assert.Nil(t, fuelingTx)
}

func TestTopUpFailedDueToSourceBalanceBelowMin(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	// Mock the sufficient balance on the auto-fueling source address, and the nonce assignment
	m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(400), nil).Once()

	// set min source balance to 1000, which is way beyond 400
	bm.minSourceBalance = big.NewInt(1000)

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	assert.Error(t, err)
	assert.Nil(t, fuelingTx)
	assert.Regexp(t, fmt.Sprintf("PD011901: Balance 400 of fueling source address %s is below the configured minimum balance 1000", bm.sourceAddress), err.Error())
}

func TestTopUpFailedDueToSourceBalanceBelowRequestedAmount(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(2000),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(500),
		MaxCost:               big.NewInt(1500),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	// Mock the sufficient balance on the auto-fueling source address, and the nonce assignment
	m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(400), nil).Once()

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	assert.Error(t, err)
	assert.Nil(t, fuelingTx)
	assert.Regexp(t, fmt.Sprintf("PD011900: Balance 400 of fueling source address %s is below the required amount 1900", bm.sourceAddress), err.Error())
}

func TestTopUpFailedDueToSourceBalanceBelowRequestedAmountConcurrencyTest(t *testing.T) {
	testConcurrency := 5000
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	// Mock no auto-fueling TX in flight
	for i := 0; i < testConcurrency; i++ {
		m.db.ExpectQuery(`SELECT.*public_txns.*data IS NULL`).
			WillReturnRows(sqlmock.NewRows([]string{}))
	}

	// Mock the sufficient balance on the auto-fueling source address, and the nonce assignment
	m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(400), nil).Once() // called once and then cached

	var wg sync.WaitGroup
	for i := 0; i < testConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fuelingTx, err := bm.TopUpAccount(ctx, &AddressAccount{
				Balance:               big.NewInt(100),
				Spent:                 big.NewInt(2000),
				Address:               *tktypes.RandAddress(),
				SpentTransactionCount: 2,
				MinCost:               big.NewInt(500),
				MaxCost:               big.NewInt(1500),
			})
			assert.Error(t, err)
			assert.Nil(t, fuelingTx)
			assert.Regexp(t, fmt.Sprintf("PD011900: Balance 400 of fueling source address %s is below the required amount 1900", bm.sourceAddress), err.Error())
		}()
	}
	wg.Wait()
}

func TestTopUpFailedDueToUnableToGetPendingFuelingTransaction(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// fail to get existing fueling tx
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnError(fmt.Errorf("pop"))

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	assert.Error(t, err)
	assert.Nil(t, fuelingTx)
	assert.Regexp(t, "pop", err.Error())
}

func TestTopUpFailedDueToUnableToGetSourceAddressBalance(t *testing.T) {
	ctx, bm, _, m, done := newTestBalanceManager(t, true, func(m *mocksAndTestControl, conf *pldconf.PublicTxManagerConfig) {
		m.disableManagerStart = true
	})
	defer done()

	testDestAddress := *tktypes.RandAddress()

	accountToTopUp := &AddressAccount{
		Balance:               big.NewInt(100),
		Spent:                 big.NewInt(200),
		Address:               testDestAddress,
		SpentTransactionCount: 2,
		MinCost:               big.NewInt(50),
		MaxCost:               big.NewInt(150),
	}
	// Mock no auto-fueling TX in flight
	m.db.ExpectQuery("SELECT.*public_txns.*data IS NULL").WillReturnRows(sqlmock.NewRows([]string{}))

	// Mock the sufficient balance on the auto-fueling source address, and the nonce assignment
	m.ethClient.On("GetBalance", mock.Anything, *bm.sourceAddress, "latest").Return(tktypes.Uint64ToUint256(0), fmt.Errorf("pop")).Once()

	fuelingTx, err := bm.TopUpAccount(ctx, accountToTopUp)
	assert.Error(t, err)
	assert.Nil(t, fuelingTx)
	assert.Regexp(t, "pop", err.Error())
}
