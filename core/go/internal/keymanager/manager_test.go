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

package keymanager

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/hyperledger/firefly-signer/pkg/secp256k1"
	"github.com/kaleido-io/paladin/config/pkg/pldconf"
	"github.com/kaleido-io/paladin/core/internal/components"
	"github.com/kaleido-io/paladin/core/mocks/componentmocks"
	"github.com/kaleido-io/paladin/core/pkg/persistence"
	"github.com/kaleido-io/paladin/core/pkg/persistence/mockpersistence"
	"github.com/kaleido-io/paladin/toolkit/pkg/algorithms"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/signpayloads"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/kaleido-io/paladin/toolkit/pkg/verifiers"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockComponents struct {
	c  *componentmocks.AllComponents
	db sqlmock.Sqlmock
}

func newTestKeyManager(t *testing.T, realDB bool, conf *pldconf.KeyManagerConfig) (context.Context, *keyManager, *mockComponents, func()) {
	ctx, cancelCtx := context.WithCancel(context.Background())
	oldLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.TraceLevel)

	mc := &mockComponents{c: componentmocks.NewAllComponents(t)}
	componentMocks := mc.c

	var p persistence.Persistence
	var pDone func()
	var err error
	if realDB {
		p, pDone, err = persistence.NewUnitTestPersistence(ctx, "keymgr")
		require.NoError(t, err)
	} else {
		mp, err := mockpersistence.NewSQLMockProvider()
		require.NoError(t, err)
		p = mp.P
		mc.db = mp.Mock
		pDone = func() {
			require.NoError(t, mp.Mock.ExpectationsWereMet())
		}
	}
	componentMocks.On("Persistence").Return(p)

	km := NewKeyManager(ctx, conf)

	ir, err := km.PreInit(mc.c)
	require.NoError(t, err)
	assert.NotNil(t, ir)

	err = km.PostInit(mc.c)
	require.NoError(t, err)

	err = km.Start()
	require.NoError(t, err)

	return ctx, km.(*keyManager), mc, func() {
		logrus.SetLevel(oldLevel)
		cancelCtx()
		km.Stop()
		pDone()
	}
}

func hdWalletConfig(name, keyPrefix string) *pldconf.WalletConfig {
	return &pldconf.WalletConfig{
		Name:        name,
		KeySelector: keyPrefix,
		Signer: &pldconf.SignerConfig{
			KeyDerivation: pldconf.KeyDerivationConfig{
				Type: pldconf.KeyDerivationTypeBIP32,
			},
			KeyStore: pldconf.KeyStoreConfig{
				Type: pldconf.KeyStoreTypeStatic,
				Static: pldconf.StaticKeyStoreConfig{
					Keys: map[string]pldconf.StaticKeyEntryConfig{
						"seed": {
							Encoding: "hex",
							Inline:   tktypes.RandHex(32),
						},
					},
				},
			},
		},
	}

}

func staticKeyConfig(name, keyPrefix string, keys ...string) *pldconf.WalletConfig {
	wc := &pldconf.WalletConfig{
		Name:        name,
		KeySelector: keyPrefix,
		Signer: &pldconf.SignerConfig{
			KeyStore: pldconf.KeyStoreConfig{
				Type: pldconf.KeyStoreTypeStatic,
				Static: pldconf.StaticKeyStoreConfig{
					Keys: map[string]pldconf.StaticKeyEntryConfig{},
				},
			},
		},
	}
	for _, keyName := range keys {
		wc.Signer.KeyStore.Static.Keys[keyName] = pldconf.StaticKeyEntryConfig{
			Encoding: "hex",
			Inline:   tktypes.RandHex(32),
		}
	}
	return wc
}

func newTestDBKeyManagerWithWallets(t *testing.T, wallets ...*pldconf.WalletConfig) (context.Context, *keyManager, *mockComponents, func()) {
	return newTestKeyManager(t, true, &pldconf.KeyManagerConfig{
		Wallets: append([]*pldconf.WalletConfig{}, wallets...),
	})
}

func TestE2ESigningHDWalletRealDB(t *testing.T) {
	ctx, km, _, done := newTestDBKeyManagerWithWallets(t,
		hdWalletConfig("hdwallet1", ""),
	)
	defer done()

	// Sub-test one - repeated resolution of a complex tree
	for i := 0; i < 4; i++ {
		// - first run creates
		// - second run validates they don't change with caching
		// - third run checks they don't change with reload
		if i == 2 {
			km.identifierCache.Clear()
			km.verifierByIdentityCache.Clear()
		}
		// - fourth run just clears the verifiers
		if i == 3 {
			km.verifierByIdentityCache.Clear()
		}

		krc := km.NewKeyResolutionContext(ctx)
		err := km.p.DB().Transaction(func(dbTX *gorm.DB) error {
			kr := krc.KeyResolver(dbTX)

			// one key out of the blue
			resolved1, err := kr.ResolveKey("bob.keys.blue.42", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
			require.NoError(t, err)
			assert.Equal(t, algorithms.ECDSA_SECP256K1, resolved1.Verifier.Algorithm)
			assert.Equal(t, verifiers.ETH_ADDRESS, resolved1.Verifier.Type)
			assert.Equal(t, "m/44'/60'/1'/0/0/0", resolved1.KeyHandle)

			// sign and recover something
			payload := []byte("some data")
			signature, err := km.Sign(ctx, resolved1, signpayloads.OPAQUE_TO_RSV, payload)
			require.NoError(t, err)
			sig, err := secp256k1.DecodeCompactRSV(ctx, signature)
			require.NoError(t, err)
			addr, err := sig.RecoverDirect(payload, 0)
			require.NoError(t, err)
			assert.Equal(t, addr.String(), resolved1.Verifier.Verifier)

			// a root key, after we've already allocated a key under it
			resolved2, err := kr.ResolveKey("bob", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
			require.NoError(t, err)
			assert.Equal(t, algorithms.ECDSA_SECP256K1, resolved2.Verifier.Algorithm)
			assert.Equal(t, verifiers.ETH_ADDRESS, resolved2.Verifier.Type)
			assert.Equal(t, "m/44'/60'/1'", resolved2.KeyHandle)

			// keys at a nested layer
			for i := 0; i < 10; i++ {
				resolved, err := kr.ResolveKey(fmt.Sprintf("bob.keys.red.%d", i), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				assert.Equal(t, algorithms.ECDSA_SECP256K1, resolved.Verifier.Algorithm)
				assert.Equal(t, verifiers.ETH_ADDRESS, resolved.Verifier.Type)
				assert.Equal(t, fmt.Sprintf("m/44'/60'/1'/0/1/%d", i), resolved.KeyHandle)
			}

			// same keys backwards
			for i := 9; i >= 0; i-- {
				resolved, err := kr.ResolveKey(fmt.Sprintf("bob.keys.red.%d", i), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				assert.Equal(t, algorithms.ECDSA_SECP256K1, resolved.Verifier.Algorithm)
				assert.Equal(t, verifiers.ETH_ADDRESS, resolved.Verifier.Type)
				assert.Equal(t, fmt.Sprintf("m/44'/60'/1'/0/1/%d", i), resolved.KeyHandle)
			}

			// keys under a different root
			for i := 0; i < 10; i++ {
				resolved, err := kr.ResolveKey(fmt.Sprintf("sally.%d", i), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				assert.Equal(t, algorithms.ECDSA_SECP256K1, resolved.Verifier.Algorithm)
				assert.Equal(t, verifiers.ETH_ADDRESS, resolved.Verifier.Type)
				assert.Equal(t, fmt.Sprintf("m/44'/60'/2'/%d", i), resolved.KeyHandle)
			}

			return krc.PreCommit()
		})
		require.NoError(t, err)
		krc.Close(true) // note a cheat in this unit test to not have a defer on this in a sub-function
	}

	// Sub-test two - concurrent resolution with a consistent outcome
	testUUIDs := make([]uuid.UUID, 15)
	for i := 0; i < len(testUUIDs); i++ {
		testUUIDs[i] = uuid.New()
	}
	const threadCount = 10
	results := make([]map[uuid.UUID]string, threadCount)

	// With this slightly more realistic example of use, we do a proper
	// defer style processing like all the code that uses us in anger should
	// do, ensuring we either commit or cancel.
	testResolveOne := func(identifier string) string {
		resolved, err := km.ResolveEthAddressBatchNewDatabaseTX(ctx, []string{identifier})
		require.NoError(t, err)
		return resolved[0].String()
	}

	wg := new(sync.WaitGroup)
	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		results[i] = make(map[uuid.UUID]string)
		go func() {
			defer wg.Done()
			for _, u := range testUUIDs {
				results[i][u] = testResolveOne(fmt.Sprintf("sally.rand.%s", u))
			}
		}()
	}
	wg.Wait()
	reference := results[0]
	for i := 0; i < threadCount; i++ {
		for _, u := range testUUIDs {
			result, found := results[i][u]
			require.True(t, found)
			require.Equal(t, reference[u], result)
			// Check the reverse lookup too
			resolved, err := km.ReverseKeyLookup(ctx, km.p.DB(), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, result)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("sally.rand.%s", u), resolved.Identifier)
		}
	}

	testResolveMulti := func(doResolve func(kr components.KeyResolver)) {
		krc := km.NewKeyResolutionContext(ctx)
		committed := false
		defer func() { krc.Close(committed) }()
		// DB TX for each UUID to hammer things a little
		err := km.p.DB().Transaction(func(dbTX *gorm.DB) error {
			doResolve(krc.KeyResolver(dbTX))
			return krc.PreCommit()
		})
		require.NoError(t, err)
		committed = true
	}

	// Now this last one is really hard.
	// We're trying to create parallelism, but have the potential for an A-B B-A deadlock.
	// Requires PostgreSQL to create the conditions for a hang (multiple parallel DB transactions)
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			testResolveMulti(func(kr components.KeyResolver) {
				_, err := kr.ResolveKey(fmt.Sprintf("path.to.A.%d", i+1000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = kr.ResolveKey(fmt.Sprintf("path.to.B.%d", i+1000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = kr.ResolveKey(fmt.Sprintf("path.to.C.%d", i+1000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
			})
		}()
		go func() {
			defer wg.Done()
			testResolveMulti(func(kr components.KeyResolver) {
				_, err := kr.ResolveKey(fmt.Sprintf("path.to.B.%d", i+2000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = kr.ResolveKey(fmt.Sprintf("path.to.C.%d", i+2000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = kr.ResolveKey(fmt.Sprintf("path.to.A.%d", i+2000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
			})
		}()
		go func() {
			defer wg.Done()
			testResolveMulti(func(lr components.KeyResolver) {
				_, err := lr.ResolveKey(fmt.Sprintf("path.to.C.%d", i+3000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = lr.ResolveKey(fmt.Sprintf("path.to.B.%d", i+3000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
				_, err = lr.ResolveKey(fmt.Sprintf("path.to.A.%d", i+3000), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
				require.NoError(t, err)
			})
		}()
		wg.Wait()
	}

}

func TestE2EMixedKeyResolution(t *testing.T) {
	staticKeys := staticKeyConfig("static", `^static\..*$`, "static.key1", "static.key2")

	ctx, km, _, done := newTestDBKeyManagerWithWallets(t,
		staticKeys,
		hdWalletConfig("hdwallet1", ""),
	)
	defer done()

	krc := km.NewKeyResolutionContextLazyDB(ctx)
	_, err := krc.KeyResolverLazyDB().ResolveKey("static.key3", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
	assert.Regexp(t, "PD020818", err)
	krc.Rollback()

	krc = km.NewKeyResolutionContextLazyDB(ctx)
	mappingStaticKey1, err := krc.KeyResolverLazyDB().ResolveKey("static.key1", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
	require.NoError(t, err)
	mappingStaticKey2, err := krc.KeyResolverLazyDB().ResolveKey("static.key2", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
	require.NoError(t, err)
	err = krc.Commit()
	require.NoError(t, err)

	key1 := secp256k1.KeyPairFromBytes(tktypes.MustParseHexBytes(staticKeys.Signer.KeyStore.Static.Keys["static.key1"].Inline))
	require.Equal(t, key1.Address.String(), mappingStaticKey1.Verifier.Verifier)

	key2 := secp256k1.KeyPairFromBytes(tktypes.MustParseHexBytes(staticKeys.Signer.KeyStore.Static.Keys["static.key2"].Inline))
	require.Equal(t, key2.Address.String(), mappingStaticKey2.Verifier.Verifier)

	krc = km.NewKeyResolutionContextLazyDB(ctx)
	_, err = krc.KeyResolverLazyDB().ResolveKey("anything.at.any.level", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
	assert.NoError(t, err)
	err = krc.Commit()
	require.NoError(t, err)

}

func TestPostInitFailures(t *testing.T) {

	mc := &mockComponents{c: componentmocks.NewAllComponents(t)}
	db, err := mockpersistence.NewSQLMockProvider()
	require.NoError(t, err)
	mc.c.On("Persistence").Return(db.P)

	km := NewKeyManager(context.Background(), &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{
			{ /* no name */ },
		},
	})
	_, err = km.PreInit(mc.c)
	require.NoError(t, err)
	err = km.PostInit(mc.c)
	assert.Regexp(t, "PD010508", err) // no name

	km = NewKeyManager(context.Background(), &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{
			hdWalletConfig("duplicated", ""),
			hdWalletConfig("duplicated", ""),
		},
	})
	_, err = km.PreInit(mc.c)
	require.NoError(t, err)
	err = km.PostInit(mc.c)
	assert.Regexp(t, "PD010509", err) // duplicate name

}

func TestSignUnknownWallet(t *testing.T) {

	ctx, km, _, done := newTestDBKeyManagerWithWallets(t)
	defer done()

	_, err := km.Sign(ctx, &pldapi.KeyMappingAndVerifier{KeyMappingWithPath: &pldapi.KeyMappingWithPath{KeyMapping: &pldapi.KeyMapping{
		Wallet: "unknown",
	}}}, signpayloads.OPAQUE_TO_RSV, []byte{})
	assert.Regexp(t, "PD010503", err)

}

func TestTimeoutWaitingForLock(t *testing.T) {

	ctx, km, _, done := newTestDBKeyManagerWithWallets(t, hdWalletConfig("wallet1", ""))
	defer done()

	readyToTry := make(chan struct{})
	waitDone := make(chan struct{})
	krc1 := km.NewKeyResolutionContext(ctx)
	go func() {

		committed := false
		defer func() {
			krc1.Close(committed)
		}()
		err := km.p.DB().Transaction(func(tx *gorm.DB) error {
			mapping1, err := krc1.KeyResolver(tx).ResolveKey("key1", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
			require.NoError(t, err)
			require.NotEmpty(t, mapping1.Verifier.Verifier)
			close(readyToTry)
			<-waitDone
			return nil
		})
		require.NoError(t, err)
		committed = true
	}()

	// Wait until we know we are blocked
	<-readyToTry
	cancelled, cancelCtx := context.WithCancel(ctx)
	cancelCtx()
	krc2 := km.NewKeyResolutionContext(cancelled)
	kr2 := krc2.KeyResolver(km.p.DB()).(*keyResolver)
	err := km.takeAllocationLock(kr2)
	assert.Regexp(t, "PD010301", err)

	close(waitDone)

	// Double unlock is a warned no-op
	km.unlockAllocation(kr2)

}

type testSigner struct {
	getMinimumKeyLen func(ctx context.Context, algorithm string) (int, error)
	getVerifier      func(ctx context.Context, algorithm string, verifierType string, privateKey []byte) (string, error)
	sign             func(ctx context.Context, algorithm string, payloadType string, privateKey []byte, payload []byte) ([]byte, error)
}

func (ts *testSigner) GetMinimumKeyLen(ctx context.Context, algorithm string) (int, error) {
	return ts.getMinimumKeyLen(ctx, algorithm)
}

func (ts *testSigner) GetVerifier(ctx context.Context, algorithm string, verifierType string, privateKey []byte) (string, error) {
	return ts.getVerifier(ctx, algorithm, verifierType, privateKey)
}

func (ts *testSigner) Sign(ctx context.Context, algorithm string, payloadType string, privateKey []byte, payload []byte) ([]byte, error) {
	return ts.sign(ctx, algorithm, payloadType, privateKey, payload)
}

func TestAddInMemorySignerAndSign(t *testing.T) {

	ctx, km, _, done := newTestDBKeyManagerWithWallets(t, hdWalletConfig("wallet1", ""))
	defer done()

	s := &testSigner{
		getVerifier: func(ctx context.Context, algorithm, verifierType string, privateKey []byte) (string, error) {
			return "custom-thing", nil
		},
	}
	km.AddInMemorySigner("test", s)

	krc := km.NewKeyResolutionContextLazyDB(ctx)
	defer krc.Rollback()

	mapping, err := krc.KeyResolverLazyDB().ResolveKey("my-custom-thing", "test:blue", "thingies")
	require.NoError(t, err)
	require.Equal(t, "test:blue", mapping.Verifier.Algorithm)
	require.Equal(t, "thingies", mapping.Verifier.Type)
	require.Equal(t, "custom-thing", mapping.Verifier.Verifier)

}

func TestResolveKeyNewDatabaseTXFail(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, false, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	mc.db.ExpectBegin()
	mc.db.ExpectQuery("SELECT.*key_paths").WillReturnError(fmt.Errorf("pop"))

	_, err := km.ResolveKeyNewDatabaseTX(ctx, "key1", algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS)
	assert.Regexp(t, "pop", err)
}

func TestResolveEthAddressNewDatabaseTXFail(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, false, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	mc.db.ExpectBegin()
	mc.db.ExpectQuery("SELECT.*key_paths").WillReturnError(fmt.Errorf("pop"))

	_, err := km.ResolveEthAddressNewDatabaseTX(ctx, "key1")
	assert.Regexp(t, "pop", err)
}

func TestResolveBatchNewDatabaseTXFail(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, false, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	mc.db.ExpectBegin()
	mc.db.ExpectQuery("SELECT.*key_paths").WillReturnError(fmt.Errorf("pop"))

	_, err := km.ResolveBatchNewDatabaseTX(ctx, algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, []string{"key1"})
	assert.Regexp(t, "pop", err)
}

func TestReverseKeyLookupFail(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, false, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	mc.db.ExpectQuery("SELECT.*key_verifiers").WillReturnError(fmt.Errorf("pop"))

	_, err := km.ReverseKeyLookup(ctx, mc.c.Persistence().DB(), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, tktypes.RandAddress().String())
	assert.Regexp(t, "pop", err)
}

func TestReverseKeyLookupNotFound(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, true, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	_, err := km.ReverseKeyLookup(ctx, mc.c.Persistence().DB(), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, tktypes.RandAddress().String())
	assert.Regexp(t, "PD010511", err)
}

func TestReverseKeyLookupFailMapping(t *testing.T) {
	ctx, km, mc, done := newTestKeyManager(t, false, &pldconf.KeyManagerConfig{
		Wallets: []*pldconf.WalletConfig{hdWalletConfig("hdwallet1", "")},
	})
	defer done()

	verifier := tktypes.RandAddress().String()
	mc.db.ExpectQuery("SELECT.*key_verifiers").WillReturnRows(
		sqlmock.NewRows([]string{"algorithm", "type", "verifier", "identifier"}).
			AddRow(algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, verifier, "!!!!! wrong"),
	)

	_, err := km.ReverseKeyLookup(ctx, mc.c.Persistence().DB(), algorithms.ECDSA_SECP256K1, verifiers.ETH_ADDRESS, verifier)
	assert.Regexp(t, "PD010500", err)
}
