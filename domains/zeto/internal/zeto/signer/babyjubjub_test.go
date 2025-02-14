package signer

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/iden3/go-iden3-crypto/babyjub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var mockPubKey = func() *babyjub.PublicKey {
	x, _ := new(big.Int).SetString("20324599009286821207881465153085764126595806822268060878040393292028608397602", 0)
	y, _ := new(big.Int).SetString("6667720951847887467326343771312468792334056297732558024347070059459187374673", 0)
	return &babyjub.PublicKey{
		X: x,
		Y: y,
	}
}

// TestDecodeBabyJubJubPublicKey tests decoding a BabyJubJub public key
func TestDecodeBabyJubJubPublicKey(t *testing.T) {
	validPubKey := mockPubKey()
	validEncoded := EncodeBabyJubJubPublicKey(validPubKey)

	tests := []struct {
		name        string
		pubKeyHex   string
		expectErr   bool
		errContains string
	}{
		{
			name:      "successful decode",
			pubKeyHex: validEncoded,
			expectErr: false,
		},
		{
			name:        "invalid hex string",
			pubKeyHex:   "invalid_hex_string",
			expectErr:   true,
			errContains: "encoding/hex: invalid byte",
		},
		{
			name:        "wrong key length",
			pubKeyHex:   hex.EncodeToString([]byte{0x01, 0x02, 0x03}),
			expectErr:   true,
			errContains: "PD210072: Invalid compressed public key length: 3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pubKey, err := DecodeBabyJubJubPublicKey(tc.pubKeyHex)

			if tc.expectErr {
				require.Error(t, err, "expected error in test case %q", tc.name)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, pubKey)
			} else {
				require.NoError(t, err, "unexpected error in test case %q", tc.name)
				require.NotNil(t, pubKey)
				assert.Equal(t, validPubKey.X.String(), pubKey.X.String(), "X coordinate mismatch")
				assert.Equal(t, validPubKey.Y.String(), pubKey.Y.String(), "Y coordinate mismatch")
			}
		})
	}
}

// TestNewBabyJubJubPrivateKey tests creating a BabyJubJub private key
func TestNewBabyJubJubPrivateKey(t *testing.T) {

	validPrivKey := make([]byte, 32)
	for i := range validPrivKey {
		validPrivKey[i] = byte(i + 1)
	}

	tests := []struct {
		name        string
		privateKey  []byte
		expectErr   bool
		errContains string
	}{
		{
			name:       "successful private key creation",
			privateKey: validPrivKey,
			expectErr:  false,
		},
		{
			name:        "private key too short",
			privateKey:  []byte{0x01, 0x02},
			expectErr:   true,
			errContains: "PD210073: Invalid key length: 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			privKey, err := NewBabyJubJubPrivateKey(tc.privateKey)

			if tc.expectErr {
				require.Error(t, err, "expected error in test case %q", tc.name)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, privKey)
			} else {
				require.NoError(t, err, "unexpected error in test case %q", tc.name)
				require.NotNil(t, privKey)
				assert.Equal(t, validPrivKey[:], privKey[:], "private key mismatch")
			}
		})
	}
}
