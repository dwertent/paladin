package common

import (
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"github.com/iden3/go-iden3-crypto/babyjub"
	corepb "github.com/kaleido-io/paladin/domains/zeto/pkg/proto"

	"github.com/kaleido-io/paladin/domains/zeto/pkg/constants"
	"github.com/kaleido-io/paladin/toolkit/pkg/prototk"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IsFungibleNullifiersCircuit
func TestIsFungibleNullifiersCircuit(t *testing.T) {
	assert.True(t, IsFungibleNullifiersCircuit(constants.CIRCUIT_ANON_NULLIFIER))
	assert.True(t, IsFungibleNullifiersCircuit(constants.CIRCUIT_ANON_NULLIFIER_BATCH))
	assert.True(t, IsFungibleNullifiersCircuit(constants.CIRCUIT_WITHDRAW_NULLIFIER))
	assert.True(t, IsFungibleNullifiersCircuit(constants.CIRCUIT_WITHDRAW_NULLIFIER_BATCH))
	assert.False(t, IsFungibleNullifiersCircuit("other"))
}

// IsNonFungibleNullifiersCircuit
func TestIsNonFungibleNullifiersCircuit(t *testing.T) {
	assert.True(t, IsNonFungibleNullifiersCircuit(constants.CIRCUIT_NF_ANON_NULLIFIER))
	assert.False(t, IsNonFungibleNullifiersCircuit("other"))
}

// IsNonFungibleCircuit
func TestIsNonFungibleCircuit(t *testing.T) {
	assert.True(t, IsNonFungibleCircuit(constants.CIRCUIT_NF_ANON))
	assert.True(t, IsNonFungibleCircuit(constants.CIRCUIT_NF_ANON_NULLIFIER))
	assert.False(t, IsNonFungibleCircuit("other"))
}

// IsNullifiersToken
func TestIsNullifiersToken(t *testing.T) {
	assert.True(t, IsNullifiersToken(constants.TOKEN_ANON_NULLIFIER))
	assert.True(t, IsNullifiersToken(constants.TOKEN_NF_ANON_NULLIFIER))
	assert.False(t, IsNullifiersToken("other"))
}

// HexUint256To32ByteHexString
func TestHexUint256To32ByteHexString(t *testing.T) {
	// Create a big.Int and cast it to *tktypes.HexUint256.
	x := big.NewInt(7890)
	hexUint := (*tktypes.HexUint256)(x)
	result := HexUint256To32ByteHexString(hexUint)
	expected := hex.EncodeToString(x.FillBytes(make([]byte, 32)))
	assert.Equal(t, expected, result)
}

// IntTo32ByteSlice
func TestIntTo32ByteSlice(t *testing.T) {
	x := big.NewInt(123456)
	bs := IntTo32ByteSlice(x)
	assert.Len(t, bs, 32)
	expected := x.FillBytes(make([]byte, 32))
	assert.Equal(t, expected, bs)
}

// IntTo32ByteHexString
func TestIntTo32ByteHexString(t *testing.T) {
	x := big.NewInt(123456)
	hexStr := IntTo32ByteHexString(x)
	expected := hex.EncodeToString(x.FillBytes(make([]byte, 32)))
	assert.Equal(t, expected, hexStr)
}

// EncodeProof: Create a dummy SnarkProof and verify the output.
func TestEncodeProof(t *testing.T) {
	// Create a dummy proof using the real corepb types.
	proof := &corepb.SnarkProof{
		A: []string{"a1", "a2"},
		B: []*corepb.B_Item{
			{Items: []string{"b00", "b01"}},
			{Items: []string{"b10", "b11"}},
		},
		C: []string{"c1", "c2"},
	}
	result := EncodeProof(proof)
	expected := map[string]interface{}{
		"pA": []string{"a1", "a2"},
		"pB": [][]string{
			{"b01", "b00"},
			{"b11", "b10"},
		},
		"pC": []string{"c1", "c2"},
	}
	assert.Equal(t, expected, result)
}

// TestEncodeTransactionData uses a table-driven approach to test both valid and invalid scenarios.
func TestEncodeTransactionData(t *testing.T) {
	tests := map[string]struct {
		transactionId   string
		transactionData ethtypes.HexBytes0xPrefix
		expected        tktypes.HexBytes
		expectError     bool
	}{
		"valid": {
			transactionId:   "0x1234",
			transactionData: ethtypes.HexBytes0xPrefix{0xab, 0xcd},
			// Expected: transactionData appended with the parsed TransactionId.
			// "0x1234" → []byte{0x12, 0x34} so the expected result is {0xab, 0xcd, 0x12, 0x34}
			expected:    []byte{0xab, 0xcd, 0x12, 0x34},
			expectError: false,
		},
		"invalid TransactionId": {
			transactionId:   "invalid",
			transactionData: ethtypes.HexBytes0xPrefix{0xab, 0xcd},
			expected:        nil,
			expectError:     true,
		},
	}

	ctx := context.Background()
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			txn := &prototk.TransactionSpecification{
				TransactionId: tc.transactionId,
			}
			result, err := EncodeTransactionData(ctx, txn, tc.transactionData)
			if tc.expectError {
				assert.Error(t, err, "expected an error when transactionId is invalid")
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err, "expected no error for valid transactionId")
				assert.Equal(t, tc.expected, result, "result should equal the expected concatenation")
			}
		})
	}
}

func TestLoadBabyJubKey(t *testing.T) {
	var validComp babyjub.PublicKeyComp
	for i := range validComp {
		// Use a non-zero value (here simply i+1) for each byte.
		validComp[i] = byte(i + 1)
	}
	// Marshal the PublicKeyComp to text to obtain the payload.
	validPayload, err := validComp.MarshalText()
	require.NoError(t, err, "failed to marshal valid PublicKeyComp")

	tests := map[string]struct {
		payload     []byte
		expectError bool
	}{
		"invalid hex": {
			payload:     []byte("zzz"),
			expectError: true,
		},
		"success": {
			payload:     validPayload,
			expectError: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pk, err := LoadBabyJubKey(tc.payload)
			if tc.expectError {
				assert.Error(t, err, "expected an error for payload %q", tc.payload)
				assert.Nil(t, pk, "expected public key to be nil on error")
			} else {
				assert.NoError(t, err, "unexpected error for payload %q", tc.payload)
				require.NotNil(t, pk, "expected a valid public key")
				// Re-compress the returned public key.
				reCompressed := pk.Compress()
				assert.Equal(t, validComp, reCompressed, "re-compressed key should equal original")
			}
		})
	}
}
func TestCryptoRandBN254(t *testing.T) {
	fieldModulus, ok := new(big.Int).SetString(modulus, 10)
	require.True(t, ok, "failed to parse field modulus")

	// Run multiple iterations to test the randomness and verify the range.
	const iterations = 10
	for i := 0; i < iterations; i++ {
		tokenValue, err := CryptoRandBN254()
		assert.NoError(t, err, "CryptoRandBN254 returned an error on iteration %d", i)
		require.NotNil(t, tokenValue, "CryptoRandBN254 returned a nil token on iteration %d", i)

		// Ensure the generated token is in the range [0, fieldModulus).
		// tokenValue must be less than fieldModulus.
		assert.Less(t, tokenValue.Cmp(fieldModulus), 0, "token value %s is not less than field modulus %s on iteration %d",
			tokenValue.String(), fieldModulus.String(), i)
	}
}
