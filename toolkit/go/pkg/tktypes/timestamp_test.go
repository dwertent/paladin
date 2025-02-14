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
// Unless required by applicable law or agreed to in writing, sotsware
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tktypes

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type UTTimeTest struct {
	T1 *Timestamp `json:"t1"`
	T2 *Timestamp `json:"t2,omitempty"`
	T3 *Timestamp `json:"t3,omitempty"`
	T4 *Timestamp `json:"t4"`
	T5 *Timestamp `json:"t5,omitempty"`
	T6 *Timestamp `json:"t6,omitempty"`
	T7 *Timestamp `json:"t7,omitempty"`
}

func TestTimestampJSONSerialization(t *testing.T) {
	now := TimestampNow()
	zeroTime := Timestamp(0)
	assert.True(t, zeroTime.Time().UnixNano() == 0)
	t6 := TimestampFromUnix(1621103852123456789)
	t7 := TimestampFromUnix(1621103797)
	utTimeTest := &UTTimeTest{
		T1: nil,
		T2: nil,
		T3: &zeroTime,
		T4: &zeroTime,
		T5: &now,
		T6: &t6,
		T7: &t7,
	}
	b, err := json.Marshal(&utTimeTest)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(
		`{"t1":null,"t3":null,"t4":null,"t5":"%s","t6":"2021-05-15T18:37:32.123456789Z","t7":"2021-05-15T18:36:37Z"}`,
		now.Time().UTC().Format(time.RFC3339Nano)), string(b))

	var utTimeTest2 UTTimeTest
	err = json.Unmarshal(b, &utTimeTest2)
	require.NoError(t, err)
	assert.Nil(t, utTimeTest.T1)
	assert.Nil(t, utTimeTest.T2)
	assert.Nil(t, nil, utTimeTest.T3)
	assert.Nil(t, nil, utTimeTest.T4)
	assert.Equal(t, now, *utTimeTest.T5)
	assert.Equal(t, t6, *utTimeTest.T6)
	assert.Equal(t, t7, *utTimeTest.T7)
}

func TestTimestampJSONUnmarshalFail(t *testing.T) {
	var utTimeTest UTTimeTest
	err := json.Unmarshal([]byte(`{"t1": "!Badness"}`), &utTimeTest)
	assert.Regexp(t, "PD020019", err)
}

func TestTimestampJSONUnmarshalNumber(t *testing.T) {
	var utTimeTest UTTimeTest
	err := json.Unmarshal([]byte(`{"t1": 981173106}`), &utTimeTest)
	require.NoError(t, err)
	assert.Equal(t, "2001-02-03T04:05:06Z", utTimeTest.T1.String())
}

func TestTimestampDatabaseSerialization(t *testing.T) {
	now := TimestampNow()
	zero := Timestamp(0)

	ts := &zero
	v, err := ts.Value()
	require.NoError(t, err)
	assert.Equal(t, int64(0), v)

	ts = &now
	v, err = ts.Value()
	require.NoError(t, err)
	assert.Equal(t, now.UnixNano(), v)

	ts1 := TimestampFromUnix(1621103852123456789)
	v, err = ts1.Value()
	require.NoError(t, err)
	assert.Equal(t, int64(1621103852123456789), v)

	ts2 := TimestampFromUnix(1621103797)
	v, err = ts2.Value()
	require.NoError(t, err)
	assert.Equal(t, int64(1621103797000000000), v)

}

func TestStringZero(t *testing.T) {
	zero := Timestamp(0)
	ts := &zero
	assert.Equal(t, "", ts.String()) // empty string rather than epoch 1970 time
}

func TestSafeEqual(t *testing.T) {
	assert.True(t, (*Timestamp)(nil).Equal(nil))
	ts1 := TimestampNow()
	assert.False(t, ts1.Equal(nil))
	ts2 := TimestampNow()
	assert.False(t, (*Timestamp)(nil).Equal(&ts2))
	ts1 = TimestampNow()
	ts2 = ts1
	assert.True(t, ts1.Equal(&ts2))
}

func TestTimestampParseValue(t *testing.T) {

	var ts Timestamp

	// Unix Nanosecs
	err := ts.Scan("1621108144123456789")
	require.NoError(t, err)
	assert.Equal(t, "2021-05-15T19:49:04.123456789Z", ts.String())
	assert.Equal(t, "2021-05-15T19:49:04.123456789Z", ts.Time().UTC().Format(time.RFC3339Nano))

	// Unix Millis
	err = ts.Scan("1621108144123")
	require.NoError(t, err)
	assert.Equal(t, "2021-05-15T19:49:04.123Z", ts.String())
	assert.Equal(t, "2021-05-15T19:49:04.123Z", ts.Time().UTC().Format(time.RFC3339Nano))

	// Unix Millis beyond precision float64 handles well, proving we use strings for parsing numbers
	err = ts.Scan("1726545933211347000")
	require.NoError(t, err)
	assert.Equal(t, int64(1726545933211347000), ts.UnixNano())

	// Unix Secs
	err = ts.Scan("1621108144")
	require.NoError(t, err)
	assert.Equal(t, "2021-05-15T19:49:04Z", ts.String())

	// RFC3339 Secs - timezone to UTC
	err = ts.Scan("2021-05-15T19:49:04-05:00")
	require.NoError(t, err)
	assert.Equal(t, "2021-05-16T00:49:04Z", ts.String())

	// RFC3339 Nanosecs
	err = ts.Scan("2021-05-15T19:49:04.123456789Z")
	require.NoError(t, err)
	assert.Equal(t, "2021-05-15T19:49:04.123456789Z", ts.String())

	// A bad string
	err = ts.Scan("!a supported time format")
	assert.Regexp(t, "PD020019", err)

	// Nil
	err = ts.Scan(nil)
	require.NoError(t, nil, err)
	assert.True(t, ts == 0)

	// Zero
	err = ts.Scan(int64(0))
	require.NoError(t, nil, err)
	assert.True(t, ts == 0)

	// Unix time
	err = ts.Scan(int64(1621108144123))
	require.NoError(t, nil, err)
	assert.Equal(t, "2021-05-15T19:49:04.123Z", ts.String())

	// A bad type
	err = ts.Scan(false)
	assert.Regexp(t, "PD020018", err)

}
