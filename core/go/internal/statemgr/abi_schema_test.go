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
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/hyperledger/firefly-signer/pkg/abi"
	"github.com/hyperledger/firefly-signer/pkg/eip712"
	"github.com/kaleido-io/paladin/core/internal/components"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testABIParam(t *testing.T, jsonParam string) *abi.Parameter {
	var a abi.Parameter
	err := json.Unmarshal([]byte(jsonParam), &a)
	require.NoError(t, err)
	return &a
}

// This is an E2E test using the actual database, the flush-writer DB storage system, and the schema cache
func TestStoreRetrieveABISchema(t *testing.T) {

	ctx, ss, done := newDBTestStateManager(t)
	defer done()

	as, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Name:    "field1",
				Type:    "uint256", // too big for an integer label, gets a 64 char hex string
				Indexed: true,
			},
			{
				Name:    "field2",
				Type:    "string",
				Indexed: true,
			},
			{
				Name:    "field3",
				Type:    "int64", // fits as an integer label
				Indexed: true,
			},
			{
				Name:    "field4",
				Type:    "bool",
				Indexed: true,
			},
			{
				Name:    "field5",
				Type:    "address",
				Indexed: true,
			},
			{
				Name:    "field6",
				Type:    "int256",
				Indexed: true,
			},
			{
				Name:    "field7",
				Type:    "bytes",
				Indexed: true,
			},
			{
				Name:    "field8",
				Type:    "uint32",
				Indexed: true,
			},
			{
				Name: "field9",
				Type: "string",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, components.SchemaTypeABI, as.Persisted().Type)
	assert.Equal(t, components.SchemaTypeABI, as.Type())
	assert.NotNil(t, as.definition)
	assert.Equal(t, "type=MyStruct(uint256 field1,string field2,int64 field3,bool field4,address field5,int256 field6,bytes field7,uint32 field8,string field9),labels=[field1,field2,field3,field4,field5,field6,field7,field8]", as.Persisted().Signature)
	cacheKey := "domain1/0xcf41493c8bb9652d1483ee6cb5122efbec6fbdf67cc27363ba5b030b59244cad"
	assert.Equal(t, cacheKey, schemaCacheKey(as.Persisted().DomainName, as.Persisted().ID))

	err = ss.persistSchemas([]*components.SchemaPersisted{as.SchemaPersisted})
	require.NoError(t, err)
	schemaID := as.Persisted().ID.String()
	contractAddress := tktypes.RandAddress()

	// Check it handles data
	state1, err := ss.PersistState(ctx, "domain1", *contractAddress, schemaID, tktypes.RawJSON(`{
		"field1": "0x0123456789012345678901234567890123456789",
		"field2": "hello world",
		"field3": 42,
		"field4": true,
		"field5": "0x687414C0B8B4182B823Aec5436965cf19b197386",
		"field6": "-10203040506070809",
		"field7": "0xfeedbeef",
		"field8": 12345,
		"field9": "things and stuff",
		"cruft": "to remove"
	}`), nil)
	require.NoError(t, err)
	assert.Equal(t, []*components.StateLabel{
		// uint256 written as zero padded string
		{DomainName: "domain1", State: state1.ID, Label: "field1", Value: "0000000000000000000000000123456789012345678901234567890123456789"},
		// string written as it is
		{DomainName: "domain1", State: state1.ID, Label: "field2", Value: "hello world"},
		// address is really a uint160, so that's how we handle it
		{DomainName: "domain1", State: state1.ID, Label: "field5", Value: "000000000000000000000000687414c0b8b4182b823aec5436965cf19b197386"},
		// int256 needs an extra byte ahead of the zero-padded string to say it's negative,
		// and is two's complement for that negative number so less negative number are string "higher"
		{DomainName: "domain1", State: state1.ID, Label: "field6", Value: "0ffffffffffffffffffffffffffffffffffffffffffffffffffdbc0638301b8e7"},
		// bytes are just bytes
		{DomainName: "domain1", State: state1.ID, Label: "field7", Value: "feedbeef"},
	}, state1.Labels)
	assert.Equal(t, []*components.StateInt64Label{
		// int64 can just be stored directly in a numeric index
		{DomainName: "domain1", State: state1.ID, Label: "field3", Value: 42},
		// bool also gets an efficient numeric index - we don't attempt to allocate anything smaller than int64 to this
		{DomainName: "domain1", State: state1.ID, Label: "field4", Value: 1},
		// uint32 also
		{DomainName: "domain1", State: state1.ID, Label: "field8", Value: 12345},
	}, state1.Int64Labels)
	assert.Equal(t, "0x90c1f63e32a708ef59b3708c57d165a87bddf758709313c57448e85a10c59544", state1.ID.String())

	// Check we get all the data in the canonical format, with the cruft removed
	assert.JSONEq(t, `{
		"field1": "6495562831695638750381182724034531561381914505",
		"field2": "hello world",
		"field3": "42",
		"field4": true,
		"field5": "0x687414c0b8b4182b823aec5436965cf19b197386",
		"field6": "-10203040506070809",
		"field7": "0xfeedbeef",
		"field8": "12345",
		"field9": "things and stuff"
	}`, string(state1.Data))

	// Second should succeed, but not do anything
	err = ss.persistSchemas([]*components.SchemaPersisted{as.SchemaPersisted})
	require.NoError(t, err)
	schemaID = as.IDString()

	getValidate := func() {
		as1, err := ss.GetSchema(ctx, as.Persisted().DomainName, schemaID, true)
		require.NoError(t, err)
		assert.NotNil(t, as1)
		as1Sig, err := as1.(*abiSchema).FullSignature(ctx)
		require.NoError(t, err)
		assert.Equal(t, as1.Persisted().Signature, as1Sig)
	}

	// Get should be from the cache
	getValidate()

	// Next from the DB
	ss.abiSchemaCache.Delete(cacheKey)
	getValidate()

	// Again from the cache
	getValidate()

	// Get the state back too
	state1a, err := ss.GetState(ctx, as.Persisted().DomainName, *contractAddress, state1.ID.String(), true, true)
	require.NoError(t, err)
	assert.Equal(t, state1.State, state1a)

	// Do a query on just one state, based on all the label fields
	var query *query.QueryJSON
	err = json.Unmarshal(([]byte)(`{
		"eq": [
		  {"field":"field1","value":"0x0123456789012345678901234567890123456789"},
		  {"field":"field2","value":"hello world"},
		  {"field":"field3","value":42},
		  {"field":"field4","value":true},
		  {"field":"field5","value":"0x687414C0B8B4182B823Aec5436965cf19b197386"},
		  {"field":"field6","value":"-10203040506070809"},
		  {"field":"field7","value":"0xfeedbeef"},
		  {"field":"field8","value":12345}
		]
	}`), &query)
	require.NoError(t, err)
	states, err := ss.FindStates(ctx, as.Persisted().DomainName, *contractAddress, schemaID, query, "all")
	require.NoError(t, err)
	assert.Len(t, states, 1)

	// Do a query that should fail on a string based label
	err = json.Unmarshal(([]byte)(`{
		"eq": [
		  {"field":"field2","value":"hello sun"}
		]
	}`), &query)
	require.NoError(t, err)
	states, err = ss.FindStates(ctx, as.Persisted().DomainName, *contractAddress, schemaID, query, "all")
	require.NoError(t, err)
	assert.Len(t, states, 0)

	// Do a query that should fail on an integer base label
	err = json.Unmarshal(([]byte)(`{
		"eq": [
		  {"field":"field3","value":43}
		]
	}`), &query)
	require.NoError(t, err)
	states, err = ss.FindStates(ctx, as.Persisted().DomainName, *contractAddress, schemaID, query, "all")
	require.NoError(t, err)
	assert.Len(t, states, 0)
}

func TestNewABISchemaInvalidTypedDataType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Name: "field1",
				Type: "function",
			},
		},
	})
	assert.Regexp(t, "FF22072", err)

}

func TestGetSchemaInvalidJSON(t *testing.T) {
	ctx, ss, mdb, done := newDBMockStateManager(t)
	defer done()

	mdb.ExpectQuery("SELECT.*schemas").WillReturnRows(sqlmock.NewRows(
		[]string{"type", "content"},
	).AddRow(components.SchemaTypeABI, "!!! { bad json"))

	_, err := ss.GetSchema(ctx, "domain1", tktypes.Bytes32Keccak(([]byte)("test")).String(), true)
	assert.Regexp(t, "PD010113", err)
}

func TestRestoreABISchemaInvalidType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchemaFromDB(ctx, &components.SchemaPersisted{
		Definition: tktypes.RawJSON(`{}`),
	})
	assert.Regexp(t, "PD010114", err)

}

func TestRestoreABISchemaInvalidTypeTree(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchemaFromDB(ctx, &components.SchemaPersisted{
		Definition: tktypes.RawJSON(`{"type":"tuple","internalType":"struct MyType","components":[{"type":"wrong"}]}`),
	})
	assert.Regexp(t, "FF22025.*wrong", err)

}

func TestABILabelSetupMissingName(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Indexed: true,
				Type:    "uint256",
			},
		},
	})
	assert.Regexp(t, "PD010108", err)

}

func TestABILabelSetupBadTree(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Indexed: true,
				Name:    "broken",
			},
		},
	})
	assert.Regexp(t, "FF22025", err)

}

func TestABILabelSetupDuplicateField(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Indexed: true,
				Name:    "field1",
				Type:    "uint256",
			},
			{
				Indexed: true,
				Name:    "field1",
				Type:    "uint256",
			},
		},
	})
	assert.Regexp(t, "PD010115", err)
}

func TestABILabelSetupUnsupportedType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	_, err := newABISchema(ctx, "domain1", &abi.Parameter{
		Type:         "tuple",
		Name:         "MyStruct",
		InternalType: "struct MyStruct",
		Components: abi.ParameterArray{
			{
				Indexed:      true,
				Name:         "nested",
				InternalType: "struct MyNested",
				Type:         "tuple",
				Components:   abi.ParameterArray{},
			},
		},
	})
	assert.Regexp(t, "PD010107", err)
}

func TestABISchemaGetLabelTypeBadType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{
					Indexed:    true,
					Type:       "fixed",
					Components: abi.ParameterArray{},
				},
			},
		},
	}
	tc, err := as.definition.TypeComponentTree()
	require.NoError(t, err)

	_, err = as.getLabelType(ctx, "f1", tc.TupleChildren()[0])
	assert.Regexp(t, "PD010103", err)
}

func TestABISchemaProcessStateInvalidType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{
					Indexed: true,
					Type:    "fixed",
					Name:    "field1",
				},
			},
		},
		primaryType: "MyStruct",
		typeSet: eip712.TypeSet{
			"MyStruct": eip712.Type{
				{
					Name: "field1",
					Type: "uint256",
				},
			},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{"field1": 12345}`), nil)
	assert.Regexp(t, "PD010103", err)
}

func TestABISchemaProcessStateLabelMissing(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components:   abi.ParameterArray{},
		},
		primaryType: "MyStruct",
		typeSet: eip712.TypeSet{
			"MyStruct": eip712.Type{
				{
					Name: "field1",
					Type: "uint256",
				},
			},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{"field1": 12345}`), nil)
	assert.Regexp(t, "PD010110", err)
}

func TestABISchemaProcessStateBadDefinition(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{

		definition: &abi.Parameter{},
	}
	_, err := as.definition.TypeComponentTreeCtx(ctx)
	assert.Regexp(t, "FF22025", err)
}

func TestABISchemaProcessStateBadValue(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components:   abi.ParameterArray{},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{!!! wrong`), nil)
	assert.Regexp(t, "PD010116", err)
}

func TestABISchemaProcessStateMismatchValue(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{Name: "field1", Type: "uint256"},
			},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{"field1":{}}`), nil)
	assert.Regexp(t, "FF22030", err)
}

func TestABISchemaProcessStateEIP712Failure(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{Name: "field1", Type: "function"},
			},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{"field1":"0x753A7decf94E48a05Fa1B342D8984acA9bFaf6B2"}`), nil)
	assert.Regexp(t, "FF22073", err)
}

func TestABISchemaProcessStateDataFailure(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{Name: "field1", Type: "function"},
			},
		},
	}
	var err error
	as.tc, err = as.definition.TypeComponentTreeCtx(ctx)
	require.NoError(t, err)
	_, err = as.ProcessState(ctx, *tktypes.RandAddress(), tktypes.RawJSON(`{"field1":"0x753A7decf94E48a05Fa1B342D8984acA9bFaf6B2"}`), nil)
	assert.Regexp(t, "FF22073", err)
}

func TestABISchemaMapLabelResolverBadType(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Type:         "tuple",
			Name:         "MyStruct",
			InternalType: "struct MyStruct",
			Components: abi.ParameterArray{
				{Name: "field1", Type: "function"},
			},
		},
	}
	_, _, err := as.mapLabelResolver(ctx, "", -1)
	assert.Regexp(t, "PD010103", err)
}

func TestABISchemaMapValueToLabelTypeErrors(t *testing.T) {

	ctx, _, _, done := newDBMockStateManager(t)
	defer done()

	as := &abiSchema{
		SchemaPersisted: &components.SchemaPersisted{
			Labels: []string{"field1"},
		},
		definition: &abi.Parameter{
			Components: abi.ParameterArray{
				{Name: "field1", Type: "function"},
				{Name: "field2", Type: "uint256"},
			},
		},
	}
	tc, err := as.definition.Components[0].TypeComponentTree()
	require.NoError(t, err)
	cv, err := tc.ParseExternal("0x753A7decf94E48a05Fa1B342D8984acA9bFaf6B2")
	require.NoError(t, err)

	// bad type
	_, _, err = as.mapValueToLabel(ctx, "", -1, cv)
	assert.Regexp(t, "PD010103", err)

	// int64
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeInt64, cv)
	assert.Regexp(t, "PD010109", err)

	// int256
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeInt256, cv)
	assert.Regexp(t, "PD010109", err)

	// uint256
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeUint256, cv)
	assert.Regexp(t, "PD010109", err)

	// string
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeString, cv)
	assert.Regexp(t, "PD010109", err)

	// bool
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeBool, cv)
	assert.Regexp(t, "PD010109", err)

	tc, err = as.definition.Components[1].TypeComponentTree()
	require.NoError(t, err)
	cv, err = tc.ParseExternal("0x12345")
	require.NoError(t, err)

	// bytes
	_, _, err = as.mapValueToLabel(ctx, "", labelTypeBytes, cv)
	assert.Regexp(t, "PD010109", err)

}