//  Copyright 2013-Present Couchbase, Inc.
//
//  Use of this software is governed by the Business Source License included
//  in the file licenses/BSL-Couchbase.txt.  As of the Change Date specified
//  in that file, in accordance with the Business Source License, use of this
//  software will be governed by the Apache License, Version 2.0, included in
//  the file licenses/APL2.txt.

package base

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	sgbucket "github.com/couchbase/sg-bucket"
	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetGet(t *testing.T) {
	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	val := make(map[string]interface{}, 0)
	val["foo"] = "bar"

	var rVal map[string]interface{}
	_, err := dataStore.Get(key, &rVal)
	assert.Error(t, err, "Key should not exist yet, expected error but got nil")

	err = dataStore.Set(key, 0, nil, val)
	assert.NoError(t, err, "Error calling Set()")

	_, err = dataStore.Get(key, &rVal)
	require.NoError(t, err, "Error calling Get()")
	fooVal, ok := rVal["foo"]
	require.True(t, ok, "expected property 'foo' not found")
	assert.Equal(t, "bar", fooVal)

	require.NoError(t, dataStore.Delete(key))
}

func TestSetGetRaw(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	val := []byte("bar")

	require.NoError(t, dataStore.SetRaw(key, 0, nil, val))

	rv, _, err := dataStore.GetRaw(key)
	require.NoError(t, err)
	require.Equal(t, string(val), string(rv))

	require.NoError(t, dataStore.Delete(key))
}

func TestAddRaw(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	val := []byte("bar")

	added, err := dataStore.AddRaw(key, 0, val)
	if err != nil {
		t.Errorf("Error calling AddRaw(): %v", err)
	}
	assert.True(t, added, "AddRaw returned added=false, expected true")

	rv, _, err := dataStore.GetRaw(key)
	require.NoError(t, err)
	require.Equal(t, string(val), string(rv))

	// Calling AddRaw for existing value should return added=false, no error
	added, err = dataStore.AddRaw(key, 0, val)
	if err != nil {
		t.Errorf("Error calling AddRaw(): %v", err)
	}
	assert.False(t, added, "AddRaw returned added=true for duplicate, expected false")

	require.NoError(t, dataStore.Delete(key))
}

func TestWriteCasBasic(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	val := []byte("bar2")

	cas := uint64(0)
	cas, err := dataStore.WriteCas(key, 0, 0, cas, []byte("bar"), sgbucket.Raw)
	require.NoError(t, err)

	casOut, err := dataStore.WriteCas(key, 0, 0, cas, val, sgbucket.Raw)
	require.NoError(t, err)
	require.NotEqual(t, cas, casOut)

	rv, _, err := dataStore.GetRaw(key)
	require.NoError(t, err)
	require.Equal(t, string(val), string(rv))

	require.NoError(t, dataStore.Delete(key))
}

func TestWriteCasAdvanced(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()

	casZero := uint64(0)

	// write doc to bucket, giving cas value of 0
	_, err := dataStore.WriteCas(key, 0, 0, casZero, []byte("bar"), sgbucket.Raw)
	require.NoError(t, err)

	// try to write doc to bucket, giving cas value of 0 again -- exepct a failure
	secondWriteCas, err := dataStore.WriteCas(key, 0, 0, casZero, []byte("bar"), sgbucket.Raw)
	require.Error(t, err)

	// try to write doc to bucket again, giving invalid cas value -- expect a failure
	// also, expect no retries, however there is currently no easy way to detect that.
	_, err = dataStore.WriteCas(key, 0, 0, secondWriteCas-1, []byte("bar"), sgbucket.Raw)
	require.Error(t, err)

	require.NoError(t, dataStore.Delete(key))
}

func TestUpdate(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	valInitial := []byte(`{"state":"initial"}`)
	valUpdated := []byte(`{"state":"updated"}`)

	updateFunc := func(current []byte) (updated []byte, expiry *uint32, isDelete bool, err error) {
		if len(current) == 0 {
			return valInitial, nil, false, nil
		} else {
			return valUpdated, nil, false, nil
		}
	}
	cas, err := dataStore.Update(key, 0, updateFunc)
	require.NoError(t, err)
	require.NotEqual(t, uint64(0), cas)

	var rv map[string]interface{}
	_, err = dataStore.Get(key, &rv)
	assert.NoError(t, err, "error retrieving initial value")
	state, ok := rv["state"]
	assert.True(t, ok, "expected state property not present")
	assert.Equal(t, "initial", state)

	cas, err = dataStore.Update(key, 0, updateFunc)
	require.NoError(t, err)
	require.NotEqual(t, uint64(0), cas)

	_, err = dataStore.Get(key, &rv)
	assert.NoError(t, err, "error retrieving updated value")
	state, ok = rv["state"]
	assert.True(t, ok, "expected state property not present")
	assert.Equal(t, "updated", state)

	require.NoError(t, dataStore.Delete(key))
}

func TestUpdateCASFailure(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	valInitial := []byte(`{"state":"initial"}`)
	valCasMismatch := []byte(`{"state":"casMismatch"}`)
	valUpdated := []byte(`{"state":"updated"}`)

	var rv map[string]interface{}
	_, err := dataStore.Get(key, &rv)
	if err == nil {
		t.Errorf("Key should not exist yet, expected error but got nil")
	}

	// Initialize document
	setErr := dataStore.Set(key, 0, nil, valInitial)
	assert.NoError(t, setErr)

	triggerCasFail := true
	updateFunc := func(current []byte) (updated []byte, expiry *uint32, isDelete bool, err error) {
		if triggerCasFail == true {
			// mutate the document to trigger cas failure
			setErr := dataStore.Set(key, 0, nil, valCasMismatch)
			assert.NoError(t, setErr)
			triggerCasFail = false
		}
		return valUpdated, nil, false, nil
	}

	_, err = dataStore.Update(key, 0, updateFunc)
	if err != nil {
		t.Errorf("Error calling Update: %v", err)
	}

	// verify update succeeded
	_, err = dataStore.Get(key, &rv)
	assert.NoError(t, err, "error retrieving updated value")
	state, ok := rv["state"]
	assert.True(t, ok, "expected state property not present")
	assert.Equal(t, "updated", state)

	require.NoError(t, dataStore.Delete(key))
}

func TestUpdateCASFailureOnInsert(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	valCasMismatch := []byte(`{"state":"casMismatch"}`)
	valInitial := []byte(`{"state":"initial"}`)

	var rv map[string]interface{}
	_, err := dataStore.Get(key, &rv)
	if err == nil {
		t.Errorf("Key should not exist yet, expected error but got nil")
	}

	// Attempt to create the doc via update
	triggerCasFail := true
	updateFunc := func(current []byte) (updated []byte, expiry *uint32, isDelete bool, err error) {
		if triggerCasFail == true {
			// mutate the document to trigger cas failure
			setErr := dataStore.Set(key, 0, nil, valCasMismatch)
			assert.NoError(t, setErr)
			triggerCasFail = false
		}
		return valInitial, nil, false, nil
	}

	_, err = dataStore.Update(key, 0, updateFunc)
	if err != nil {
		t.Errorf("Error calling Update: %v", err)
	}

	// verify update succeeded
	_, err = dataStore.Get(key, &rv)
	assert.NoError(t, err, "error retrieving updated value")
	state, ok := rv["state"]
	assert.True(t, ok, "expected state property not present")
	assert.Equal(t, "initial", state)

	require.NoError(t, dataStore.Delete(key))
}

func TestIncrCounter(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()

	defer func() {
		assert.NoError(t, dataStore.Delete(key))
	}()

	// New Counter - incr 0, default 0 - expect zero-value counter doc to be created
	value, err := dataStore.Incr(key, 0, 0, 0)
	require.NoError(t, err, "Error incrementing non-existent counter")
	require.Equal(t, uint64(0), value)

	// Retrieve existing counter value using GetCounter
	retrieval, err := GetCounter(dataStore, key)
	require.NoError(t, err, "Error retrieving value for existing counter")
	require.Equal(t, uint64(0), retrieval)

	// remove zero value so we're able to test default below
	require.NoError(t, dataStore.Delete(key))

	// New Counter - incr 1, default 5
	value, err = dataStore.Incr(key, 1, 5, 0)
	require.NoError(t, err, "Error incrementing non-existent counter")

	// key did not exist - so expect the "initial" value of 5
	require.Equal(t, uint64(5), value)

	// Retrieve existing counter value using GetCounter
	retrieval, err = GetCounter(dataStore, key)
	require.NoError(t, err, "Error retrieving value for existing counter")
	require.Equal(t, uint64(5), retrieval)

	// Increment existing counter
	retrieval, err = dataStore.Incr(key, 1, 5, 0)
	require.NoError(t, err, "Error incrementing value for existing counter")
	require.Equal(t, uint64(6), retrieval)
}

func TestGetAndTouchRaw(t *testing.T) {

	// There's no easy way to validate the expiry time of a doc (that I know of)
	// so this is just a smoke test

	key := t.Name()
	val := []byte("bar")

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	defer func() {
		assert.NoError(t, dataStore.Delete(key))
	}()

	_, _, err := dataStore.GetRaw(key)
	assert.Error(t, err, "Key should not exist yet, expected error but got nil")

	err = dataStore.SetRaw(key, 0, nil, val)
	assert.NoError(t, err, "Error calling SetRaw()")

	rv, _, err := dataStore.GetRaw(key)
	require.NoError(t, err)
	require.Equal(t, string(val), string(rv))

	rv, _, err = dataStore.GetAndTouchRaw(key, 1)
	assert.NoError(t, err, "Error calling GetAndTouchRaw")

	require.Equal(t, string(val), string(rv))
	assert.Equal(t, len(val), len(rv))

	_, err = dataStore.Touch(key, 1)
	assert.NoError(t, err, "Error calling Touch")

}

func SkipXattrTestsIfNotEnabled(t *testing.T) {

	if !TestUseXattrs() {
		t.Skip("XATTR based tests not enabled.  Enable via SG_TEST_USE_XATTRS=true environment variable")
	}
}

// TestXattrWriteCasSimple.  Validates basic write of document with xattr, and retrieval of the same doc w/ xattr.
func TestXattrWriteCasSimple(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	valBytes := MustJSONMarshal(t, val)

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, syncMutateInOpts())
	require.NoError(t, err)
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)

	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])
	macroCasString, ok := retrievedXattr[xattrMacroCas].(string)
	assert.True(t, ok, "Unable to retrieve xattrMacroCas as string")
	assert.Equal(t, cas, HexCasToUint64(macroCasString))
	macroBodyHashString, ok := retrievedXattr[xattrMacroValueCrc32c].(string)
	assert.True(t, ok, "Unable to retrieve xattrMacroValueCrc32c as string")
	assert.Equal(t, Crc32cHashString(valBytes), macroBodyHashString)

	// Validate against $document.value_crc32c
	var retrievedVxattr map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, "$document", "", &retrievedVal, &retrievedVxattr, nil)
	require.NoError(t, err)
	vxattrCrc32c, ok := retrievedVxattr["value_crc32c"].(string)
	assert.True(t, ok, "Unable to retrieve virtual xattr crc32c as string")

	assert.Equal(t, Crc32cHashString(valBytes), vxattrCrc32c)
	assert.Equal(t, macroBodyHashString, vxattrCrc32c)

}

// TestXattrWriteCasUpsert.  Validates basic write of document with xattr,  retrieval of the same doc w/ xattr, update of the doc w/ xattr, retrieval of the doc w/ xattr.
func TestXattrWriteCasUpsert(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])

	val2 := make(map[string]interface{})
	val2["body_field"] = "5678"
	xattrVal2 := make(map[string]interface{})
	xattrVal2["seq"] = float64(124)
	xattrVal2["rev"] = "2-5678"
	cas, err = dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, getCas, val2, xattrVal2, nil)
	assert.NoError(t, err, "WriteCasWithXattr error")
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal2 map[string]interface{}
	var retrievedXattr2 map[string]interface{}
	getCas, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal2, &retrievedXattr2, nil)
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal2, retrievedXattr2)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val2["body_field"], retrievedVal2["body_field"])
	assert.Equal(t, xattrVal2["seq"], retrievedXattr2["seq"])
	assert.Equal(t, xattrVal2["rev"], retrievedXattr2["rev"])

}

// TestXattrWriteCasWithXattrCasCheck.  Validates cas check when using WriteCasWithXattr
func TestXattrWriteCasWithXattrCasCheck(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["sg_field"] = "sg_value"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, "")
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["sg_field"], retrievedVal["sg_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])

	// Simulate an SDK update
	updatedVal := make(map[string]interface{})
	updatedVal["sdk_field"] = "abc"
	require.NoError(t, dataStore.Set(key, 0, nil, updatedVal))

	// Attempt to update with the previous CAS
	val["sg_field"] = "sg_value_mod"
	xattrVal["rev"] = "2-1234"
	_, err = dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, getCas, val, xattrVal, nil)
	assert.True(t, IsCasMismatch(err), "error is %v", err)

	// Retrieve again, ensure we get the SDK value, SG xattr
	retrievedVal = nil
	retrievedXattr = nil
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, nil, retrievedVal["sg_field"])
	assert.Equal(t, updatedVal["sdk_field"], retrievedVal["sdk_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, "1-1234", retrievedXattr["rev"])

}

// TestWriteCasXATTRRaw.  Validates basic write of document and xattr as raw bytes.
func TestXattrWriteCasRaw(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, MustJSONMarshal(t, val), MustJSONMarshal(t, xattrVal), nil)
	require.NoError(t, err)

	var retrievedValByte []byte
	var retrievedXattrByte []byte
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedValByte, &retrievedXattrByte, nil)
	require.NoError(t, err)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_ = json.Unmarshal(retrievedValByte, &retrievedVal)
	_ = json.Unmarshal(retrievedXattrByte, &retrievedXattr)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])
}

// TestWriteCasTombstoneResurrect.  Verifies writing a new document body and xattr to a logically deleted document (xattr still exists)
func TestXattrWriteCasTombstoneResurrect(t *testing.T) {

	if UnitTestUrlIsWalrus() {
		t.Skip("Test requires Couchbase Server bucket when using xattrs")
	}

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	// Write document with xattr
	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])

	// Delete the body (retains xattr)
	require.NoError(t, dataStore.Delete(key))

	// Update the doc and xattr
	val = make(map[string]interface{})
	val["body_field"] = "5678"
	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = float64(456)
	xattrVal["rev"] = "2-2345"
	_, err = dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	// Verify retrieval
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)

	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])
}

// TestXattrWriteCasTombstoneUpdate.  Validates update of xattr on logically deleted document.
func TestXattrWriteCasTombstoneUpdate(t *testing.T) {

	t.Skip("Test does not pass with errors: https://gist.github.com/tleyden/d261fe2b92bdaaa6e78f9f1c00fdfd58.  Needs investigation")

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(123)
	xattrVal["rev"] = "1-1234"

	// Write document with xattr
	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)
	log.Printf("Post-write, cas is %d", cas)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	log.Printf("TestWriteCasXATTR retrieved: %s, %s", retrievedVal, retrievedXattr)
	assert.Equal(t, cas, getCas)
	assert.Equal(t, val["body_field"], retrievedVal["body_field"])
	assert.Equal(t, xattrVal["seq"], retrievedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], retrievedXattr["rev"])

	require.NoError(t, dataStore.Delete(key))

	log.Printf("Deleted document")
	// Update the xattr
	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = float64(456)
	xattrVal["rev"] = "2-2345"
	_, err = dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, nil, xattrVal, nil)
	require.NoError(t, err)

	log.Printf("Updated tombstoned document")
	// Verify retrieval
	var modifiedVal map[string]interface{}
	var modifiedXattr map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &modifiedVal, &modifiedXattr, nil)
	require.NoError(t, err)
	log.Printf("Retrieved tombstoned document")
	log.Printf("TestWriteCasXATTR retrieved modified: %s, %s", modifiedVal, modifiedXattr)
	assert.Equal(t, xattrVal["seq"], modifiedXattr["seq"])
	assert.Equal(t, xattrVal["rev"], modifiedXattr["rev"])
}

// TestXattrWriteUpdateXattr.  Validates basic write of document with xattr, and retrieval of the same doc w/ xattr.
func TestXattrWriteUpdateXattr(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["counter"] = float64(1)

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = float64(1)
	xattrVal["rev"] = "1-1234"

	// Dummy write update function that increments 'counter' in the doc and 'seq' in the xattr
	writeUpdateFunc := func(doc []byte, xattr []byte, userXattr []byte, cas uint64) (
		updatedDoc []byte, updatedXattr []byte, isDelete bool, updatedExpiry *uint32, updatedSpec []sgbucket.MacroExpansionSpec, err error) {

		var docMap map[string]interface{}
		var xattrMap map[string]interface{}
		// Marshal the doc
		if len(doc) > 0 {
			err = JSONUnmarshal(doc, &docMap)
			if err != nil {
				return nil, nil, false, nil, nil, pkgerrors.Wrapf(err, "Unable to unmarshal incoming doc")
			}
		} else {
			// No incoming doc, treat as insert.
			docMap = make(map[string]interface{})
		}

		// Marshal the xattr
		if len(xattr) > 0 {
			err = JSONUnmarshal(xattr, &xattrMap)
			if err != nil {
				return nil, nil, false, nil, nil, pkgerrors.Wrapf(err, "Unable to unmarshal incoming xattr")
			}
		} else {
			// No incoming xattr, treat as insert.
			xattrMap = make(map[string]interface{})
		}

		// Update the doc
		existingCounter, ok := docMap["counter"].(float64)
		if ok {
			docMap["counter"] = existingCounter + float64(1)
		} else {
			docMap["counter"] = float64(1)
		}

		// Update the xattr
		existingSeq, ok := xattrMap["seq"].(float64)
		if ok {
			xattrMap["seq"] = existingSeq + float64(1)
		} else {
			xattrMap["seq"] = float64(1)
		}

		updatedDoc = MustJSONMarshal(t, docMap)
		updatedXattr = MustJSONMarshal(t, xattrMap)
		return updatedDoc, updatedXattr, false, nil, updatedSpec, nil
	}

	// Insert
	_, err := dataStore.WriteUpdateWithXattr(ctx, key, xattrName, "", 0, nil, nil, writeUpdateFunc)
	require.NoError(t, err)

	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	log.Printf("Retrieval after WriteUpdate insert: doc: %v, xattr: %v", retrievedVal, retrievedXattr)
	require.NoError(t, err)
	assert.Equal(t, float64(1), retrievedVal["counter"])
	assert.Equal(t, float64(1), retrievedXattr["seq"])

	// Update
	_, err = dataStore.WriteUpdateWithXattr(ctx, key, xattrName, "", 0, nil, nil, writeUpdateFunc)
	require.NoError(t, err)

	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	log.Printf("Retrieval after WriteUpdate update: doc: %v, xattr: %v", retrievedVal, retrievedXattr)

	assert.Equal(t, float64(2), retrievedVal["counter"])
	assert.Equal(t, float64(2), retrievedXattr["seq"])

}

func TestWriteUpdateWithXattrUserXattr(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	key := t.Name()
	xattrKey := SyncXattrName
	userXattrKey := "UserXattr"

	writeUpdateFunc := func(doc []byte, xattr []byte, userXattr []byte, cas uint64) (updatedDoc []byte, updatedXattr []byte, isDelete bool, updatedExpiry *uint32, updatedSpec []sgbucket.MacroExpansionSpec, err error) {

		var docMap map[string]interface{}
		var xattrMap map[string]interface{}

		if len(doc) > 0 {
			err = JSONUnmarshal(xattr, &docMap)
			if err != nil {
				return nil, nil, false, nil, nil, err
			}
		} else {
			docMap = make(map[string]interface{})
		}

		if len(xattr) > 0 {
			err = JSONUnmarshal(xattr, &xattrMap)
			if err != nil {
				return nil, nil, false, nil, nil, err
			}
		} else {
			xattrMap = make(map[string]interface{})
		}

		var userXattrMap map[string]interface{}
		if len(userXattr) > 0 {
			err = JSONUnmarshal(userXattr, &userXattrMap)
			if err != nil {
				return nil, nil, false, nil, nil, err
			}
		} else {
			userXattrMap = nil
		}

		docMap["userXattrVal"] = userXattrMap

		updatedDoc = MustJSONMarshal(t, docMap)
		updatedXattr = MustJSONMarshal(t, xattrMap)

		return updatedDoc, updatedXattr, false, nil, updatedSpec, nil
	}

	_, err := dataStore.WriteUpdateWithXattr(ctx, key, xattrKey, userXattrKey, 0, nil, nil, writeUpdateFunc)
	assert.NoError(t, err)

	var gotBody map[string]interface{}
	_, err = dataStore.Get(key, &gotBody)
	assert.NoError(t, err)
	assert.Equal(t, nil, gotBody["userXattrVal"])

	userXattrVal := map[string]interface{}{"val": "val"}

	_, err = dataStore.WriteUserXattr(key, userXattrKey, userXattrVal)
	assert.NoError(t, err)

	_, err = dataStore.WriteUpdateWithXattr(ctx, key, xattrKey, userXattrKey, 0, nil, nil, writeUpdateFunc)
	assert.NoError(t, err)

	_, err = dataStore.Get(key, &gotBody)
	assert.NoError(t, err)

	assert.Equal(t, userXattrVal, gotBody["userXattrVal"])
}

// TestXattrDeleteDocument.  Delete document that has a system xattr.  System XATTR should be retained and retrievable.
func TestXattrDeleteDocument(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	// Create document with XATTR
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	key := t.Name()
	// Create w/ XATTR, delete doc and XATTR, retrieve doc (expect fail), retrieve XATTR (expect success)
	cas := uint64(0)
	_, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	// Delete the document.
	require.NoError(t, dataStore.Delete(key))

	// Verify delete of body was successful, retrieve XATTR
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	assert.Len(t, retrievedVal, 0)
	assert.Equal(t, float64(123), retrievedXattr["seq"])

}

// TestXattrDeleteDocumentUpdate.  Delete a document that has a system xattr along with an xattr update.
func TestXattrDeleteDocumentUpdate(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	// Create document with XATTR
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 1
	xattrVal["rev"] = "1-1234"

	key := t.Name()

	// Create w/ XATTR, delete doc and XATTR, retrieve doc (expect fail), retrieve XATTR (expect success)
	cas := uint64(0)
	_, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	// Delete the document.
	require.NoError(t, dataStore.Delete(key))

	// Verify delete of body was successful, retrieve XATTR
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	getCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	assert.Len(t, retrievedVal, 0)
	assert.Equal(t, float64(1), retrievedXattr["seq"])
	log.Printf("Post-delete xattr (1): %s", retrievedXattr)
	log.Printf("Post-delete cas (1): %x", getCas)

	// Update the xattr only
	xattrVal["seq"] = 2
	xattrVal["rev"] = "1-1234"
	casOut, writeErr := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, getCas, nil, xattrVal, nil)
	assert.NoError(t, writeErr, "Error updating xattr post-delete")
	log.Printf("WriteCasWithXattr cas: %d", casOut)

	// Retrieve the document, validate cas values
	var postDeleteVal map[string]interface{}
	var postDeleteXattr map[string]interface{}
	getCas2, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &postDeleteVal, &postDeleteXattr, nil)
	assert.NoError(t, err, "Error getting document post-delete")
	assert.Equal(t, float64(2), postDeleteXattr["seq"])
	assert.Len(t, postDeleteVal, 0)
	log.Printf("Post-delete xattr (2): %s", postDeleteXattr)
	log.Printf("Post-delete cas (2): %x", getCas2)

}

// TestXattrDeleteDocumentAndUpdateXATTR.  Delete the document body and update the xattr.
func TestXattrDeleteDocumentAndUpdateXattr(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	// Create document with XATTR
	xattrName := SyncXattrName
	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	key := t.Name()

	// Create w/ XATTR, delete doc and XATTR, retrieve doc (expect fail), retrieve XATTR (expect fail)
	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	_, mutateErr := dataStore.UpdateXattrDeleteBody(ctx, key, xattrName, 0, cas, xattrVal, nil)
	assert.NoError(t, mutateErr)

	// Verify delete of body and update of XATTR
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	mutateCas, err := dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)
	assert.Len(t, retrievedVal, 0)
	assert.Equal(t, float64(123), retrievedXattr["seq"])
	log.Printf("value: %v, xattr: %v", retrievedVal, retrievedXattr)
	log.Printf("MutateInEx cas: %v", mutateCas)

}

// Validates tombstone of doc + xattr in a matrix of various possible previous states of the document.
func TestXattrTombstoneDocAndUpdateXattr(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key1 := t.Name() + "DocExistsXattrExists"
	key2 := t.Name() + "DocExistsNoXattr"
	key3 := t.Name() + "XattrExistsNoDoc"
	key4 := t.Name() + "NoDocNoXattr"

	// 1. Create document with XATTR
	val := make(map[string]interface{})
	val["type"] = key1

	xattrName := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	var err error

	// Create w/ XATTR
	cas1 := uint64(0)
	cas1, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName, 0, cas1, val, xattrVal, nil)
	require.NoError(t, err)

	// 2. Create document with no XATTR
	val = make(map[string]interface{})
	val["type"] = key2
	cas2, writeErr := dataStore.WriteCas(key2, 0, 0, 0, val, 0)
	assert.NoError(t, writeErr)

	// 3. Xattr, no document
	val = make(map[string]interface{})
	val["type"] = key3

	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = 456
	xattrVal["rev"] = "1-1234"

	// Create w/ XATTR
	cas3int := uint64(0)
	cas3int, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName, 0, cas3int, val, xattrVal, nil)
	require.NoError(t, err)
	// Delete the doc body
	cas3, removeErr := dataStore.Remove(key3, cas3int)
	if removeErr != nil {
		t.Errorf("Error removing doc body: %+v.  Cas: %v", removeErr, cas3)
	}

	// 4. No xattr, no document
	updatedVal := make(map[string]interface{})
	updatedVal["type"] = "updated"

	updatedXattrVal := make(map[string]interface{})
	updatedXattrVal["seq"] = 123
	updatedXattrVal["rev"] = "2-1234"

	xattrValBytes := MustJSONMarshal(t, updatedXattrVal)

	// Attempt to delete DocExistsXattrExists, DocExistsNoXattr, and XattrExistsNoDoc
	// No errors should be returned when deleting these.
	keys := []string{key1, key2, key3}
	casValues := []uint64{cas1, cas2, cas3}
	shouldDeleteBody := []bool{true, true, false}
	for i, key := range keys {

		log.Printf("Delete testing for key: %v", key)

		// First attempt to update with a bad cas value, and ensure we're getting the expected error
		_, errCasMismatch := dataStore.WriteWithXattr(ctx, key, xattrName, 0, uint64(1234), nil, xattrValBytes, true, shouldDeleteBody[i], nil)

		assert.True(t, IsCasMismatch(errCasMismatch), fmt.Sprintf("Expected cas mismatch for %s", key))

		_, errDelete := dataStore.WriteWithXattr(ctx, key, xattrName, 0, casValues[i], nil, xattrValBytes, true, shouldDeleteBody[i], nil)
		log.Printf("Delete error: %v", errDelete)

		assert.NoError(t, errDelete, fmt.Sprintf("Unexpected error deleting %s", key))
		assert.True(t, verifyDocDeletedXattrExists(ctx, dataStore, key, xattrName), fmt.Sprintf("Expected doc %s to be deleted", key))
	}

	// Now attempt to tombstone key4 (NoDocNoXattr), should not return an error (per SG #3307).  Should save xattr metadata.
	log.Printf("Deleting key: %v", key4)
	_, errDelete := dataStore.WriteWithXattr(ctx, key4, xattrName, 0, uint64(0), nil, xattrValBytes, true, false, nil)

	assert.NoError(t, errDelete, "Unexpected error tombstoning non-existent doc")
	assert.True(t, verifyDocDeletedXattrExists(ctx, dataStore, key4, xattrName), "Expected doc to be deleted, but xattrs to exist")

}

// Validates deletion of doc + xattr in a matrix of various possible previous states of the document.
func TestXattrDeleteDocAndXattr(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key1 := t.Name() + "DocExistsXattrExists"
	key2 := t.Name() + "DocExistsNoXattr"
	key3 := t.Name() + "XattrExistsNoDoc"
	key4 := t.Name() + "NoDocNoXattr"

	// 1. Create document with XATTR
	val := make(map[string]interface{})
	val["type"] = key1

	xattrName := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	var err error

	// Create w/ XATTR
	cas1 := uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName, 0, cas1, val, xattrVal, nil)
	require.NoError(t, err)

	// 2. Create document with no XATTR
	val = make(map[string]interface{})
	val["type"] = key2
	err = dataStore.Set(key2, uint32(0), nil, val)
	assert.NoError(t, err)

	// 3. Xattr, no document
	val = make(map[string]interface{})
	val["type"] = key3

	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = 456
	xattrVal["rev"] = "1-1234"

	// Create w/ XATTR
	cas3int := uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName, 0, cas3int, val, xattrVal, nil)
	require.NoError(t, err)
	// Delete the doc body
	require.NoError(t, dataStore.Delete(key3))

	// 4. No xattr, no document

	// Attempt to delete DocExistsXattrExists, DocExistsNoXattr, and XattrExistsNoDoc
	// No errors should be returned when deleting these.
	keys := []string{key1, key2, key3}
	for _, key := range keys {
		log.Printf("Deleting key: %v", key)
		errDelete := dataStore.DeleteWithXattr(ctx, key, xattrName)
		assert.NoError(t, errDelete, fmt.Sprintf("Unexpected error deleting %s", key))
		assert.True(t, verifyDocAndXattrDeleted(ctx, dataStore, key, xattrName), "Expected doc to be deleted")
	}

	// Now attempt to delete key4 (NoDocNoXattr), which is expected to return a Key Not Found error
	log.Printf("Deleting key: %v", key4)
	errDelete := dataStore.DeleteWithXattr(ctx, key4, xattrName)
	assert.Error(t, errDelete)
	assert.Truef(t, IsDocNotFoundError(errDelete), "Exepcted keynotfound error but got %v", errDelete)
	assert.True(t, verifyDocAndXattrDeleted(ctx, dataStore, key4, xattrName), "Expected doc to be deleted")
}

// This simulates a race condition by calling deleteWithXattrInternal() and passing a custom
// callback function
func TestDeleteWithXattrWithSimulatedRaceResurrect(t *testing.T) {

	if UnitTestUrlIsWalrus() {
		t.Skip("Test requires CBS in order to use deleteWithXattrInternal callback")
	}
	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	xattrName := SyncXattrName
	createTombstonedDoc(t, dataStore, key, xattrName)

	numTimesCalledBack := 0
	callback := func(k string, xattrKey string) {

		// Only want the callback to execute once.  Should be called multiple times (twice) due to expected
		// cas failure due to using stale cas
		if numTimesCalledBack >= 1 {
			return
		}
		numTimesCalledBack += 1

		// Resurrect the doc by updating the doc body
		updatedVal := map[string]interface{}{}
		updatedVal["foo"] = "bar"
		xattrVal := make(map[string]interface{})
		xattrVal["seq"] = float64(456)
		xattrVal["rev"] = "2-2345"
		_, writeErr := dataStore.WriteCasWithXattr(ctx, k, xattrKey, 0, 0, updatedVal, xattrVal, nil)
		require.NoError(t, writeErr)

	}

	// case to KvXattrStore to pass to deleteWithXattrInternal
	collection, ok := dataStore.(*Collection)
	require.True(t, ok)
	deleteErr := deleteWithXattrInternal(ctx, collection, key, xattrName, callback)
	assert.Equal(t, 1, numTimesCalledBack)
	assert.Error(t, deleteErr)
}

// TestXattrRetrieveDocumentAndXattr.
func TestXattrRetrieveDocumentAndXattr(t *testing.T) {

	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key1 := t.Name() + "DocExistsXattrExists"
	key2 := t.Name() + "DocExistsNoXattr"
	key3 := t.Name() + "XattrExistsNoDoc"
	key4 := t.Name() + "NoDocNoXattr"

	// 1. Create document with XATTR
	val := make(map[string]interface{})
	val["type"] = key1

	xattrName := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	var err error

	// Create w/ XATTR
	cas := uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	// 2. Create document with no XATTR
	val = make(map[string]interface{})
	val["type"] = key2
	_, err = dataStore.Add(key2, 0, val)
	require.NoError(t, err)

	// 3. Xattr, no document
	val = make(map[string]interface{})
	val["type"] = key3

	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = 456
	xattrVal["rev"] = "1-1234"

	// Create w/ XATTR
	cas = uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)
	// Delete the doc
	require.NoError(t, dataStore.Delete(key3))

	// 4. No xattr, no document

	// Attempt to retrieve all 4 docs
	var key1DocResult map[string]interface{}
	var key1XattrResult map[string]interface{}
	_, key1err := dataStore.GetWithXattr(ctx, key1, xattrName, "", &key1DocResult, &key1XattrResult, nil)
	assert.NoError(t, key1err, "Unexpected error retrieving doc w/ xattr")
	assert.Equal(t, key1, key1DocResult["type"])
	assert.Equal(t, "1-1234", key1XattrResult["rev"])

	var key2DocResult map[string]interface{}
	var key2XattrResult map[string]interface{}
	_, key2err := dataStore.GetWithXattr(ctx, key2, xattrName, "", &key2DocResult, &key2XattrResult, nil)
	assert.NoError(t, key2err, "Unexpected error retrieving doc w/out xattr")
	assert.Equal(t, key2, key2DocResult["type"])
	assert.Nil(t, key2XattrResult)

	var key3DocResult map[string]interface{}
	var key3XattrResult map[string]interface{}
	_, key3err := dataStore.GetWithXattr(ctx, key3, xattrName, "", &key3DocResult, &key3XattrResult, nil)
	assert.NoError(t, key3err, "Unexpected error retrieving doc w/out xattr")
	assert.Nil(t, key3DocResult)
	assert.Equal(t, "1-1234", key3XattrResult["rev"])

	var key4DocResult map[string]interface{}
	var key4XattrResult map[string]interface{}
	_, key4err := dataStore.GetWithXattr(ctx, key4, xattrName, "", &key4DocResult, &key4XattrResult, nil)
	assert.True(t, IsDocNotFoundError(key4err))
	assert.Nil(t, key4DocResult)
	assert.Nil(t, key4XattrResult)

}

// TestXattrMutateDocAndXattr.  Validates mutation of doc + xattr in various possible previous states of the document.
func TestXattrMutateDocAndXattr(t *testing.T) {

	// Skipping on non-Couchbase until CBG-3392 is fixed
	if UnitTestUrlIsWalrus() {
		t.Skip("Test requires Couchbase Server bucket when using xattrs")
	}
	SkipXattrTestsIfNotEnabled(t)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key1 := t.Name() + "DocExistsXattrExists"
	key2 := t.Name() + "DocExistsNoXattr"
	key3 := t.Name() + "XattrExistsNoDoc"
	key4 := t.Name() + "NoDocNoXattr"

	// 1. Create document with XATTR
	val := make(map[string]interface{})
	val["type"] = key1

	xattrName := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	var err error

	// Create w/ XATTR
	cas1 := uint64(0)
	cas1, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName, 0, cas1, val, xattrVal, nil)
	require.NoError(t, err)

	// 2. Create document with no XATTR
	val = make(map[string]interface{})
	val["type"] = key2
	cas2, err := dataStore.WriteCas(key2, 0, 0, 0, val, 0)
	require.NoError(t, err)

	// 3. Xattr, no document
	val = make(map[string]interface{})
	val["type"] = key3

	xattrVal = make(map[string]interface{})
	xattrVal["seq"] = 456
	xattrVal["rev"] = "1-1234"

	// Create w/ XATTR
	cas3int := uint64(0)
	cas3int, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName, 0, cas3int, val, xattrVal, nil)
	require.NoError(t, err)
	// Delete the doc body
	require.NoError(t, dataStore.Delete(key3))

	// 4. No xattr, no document
	cas4 := 0
	updatedVal := make(map[string]interface{})
	updatedVal["type"] = "updated"

	updatedXattrVal := make(map[string]interface{})
	updatedXattrVal["seq"] = 123
	updatedXattrVal["rev"] = "2-1234"

	// Attempt to mutate all 4 docs
	exp := uint32(0)
	updatedVal["type"] = fmt.Sprintf("updated_%s", key1)
	_, key1err := dataStore.WriteCasWithXattr(ctx, key1, xattrName, exp, cas1, &updatedVal, &updatedXattrVal, nil)
	assert.NoError(t, key1err, fmt.Sprintf("Unexpected error mutating %s", key1))
	var key1DocResult map[string]interface{}
	var key1XattrResult map[string]interface{}
	_, key1err = dataStore.GetWithXattr(ctx, key1, xattrName, "", &key1DocResult, &key1XattrResult, nil)
	require.NoError(t, key1err)
	assert.Equal(t, fmt.Sprintf("updated_%s", key1), key1DocResult["type"])
	assert.Equal(t, "2-1234", key1XattrResult["rev"])

	updatedVal["type"] = fmt.Sprintf("updated_%s", key2)
	_, key2err := dataStore.WriteCasWithXattr(ctx, key2, xattrName, exp, cas2, &updatedVal, &updatedXattrVal, nil)
	assert.NoError(t, key2err, fmt.Sprintf("Unexpected error mutating %s", key2))
	var key2DocResult map[string]interface{}
	var key2XattrResult map[string]interface{}
	_, key2err = dataStore.GetWithXattr(ctx, key2, xattrName, "", &key2DocResult, &key2XattrResult, nil)
	require.NoError(t, key2err)
	assert.Equal(t, fmt.Sprintf("updated_%s", key2), key2DocResult["type"])
	assert.Equal(t, "2-1234", key2XattrResult["rev"])

	updatedVal["type"] = fmt.Sprintf("updated_%s", key3)
	_, key3err := dataStore.WriteCasWithXattr(ctx, key3, xattrName, exp, cas3int, &updatedVal, &updatedXattrVal, nil)
	assert.NoError(t, key3err, fmt.Sprintf("Unexpected error mutating %s", key3))
	var key3DocResult map[string]interface{}
	var key3XattrResult map[string]interface{}
	_, key3err = dataStore.GetWithXattr(ctx, key3, xattrName, "", &key3DocResult, &key3XattrResult, nil)
	require.NoError(t, key3err)
	assert.Equal(t, fmt.Sprintf("updated_%s", key3), key3DocResult["type"])
	assert.Equal(t, "2-1234", key3XattrResult["rev"])

	updatedVal["type"] = fmt.Sprintf("updated_%s", key4)
	_, key4err := dataStore.WriteCasWithXattr(ctx, key4, xattrName, exp, uint64(cas4), &updatedVal, &updatedXattrVal, nil)
	assert.NoError(t, key4err, fmt.Sprintf("Unexpected error mutating %s", key4))
	var key4DocResult map[string]interface{}
	var key4XattrResult map[string]interface{}
	_, key4err = dataStore.GetWithXattr(ctx, key4, xattrName, "", &key4DocResult, &key4XattrResult, nil)
	require.NoError(t, key4err)
	assert.Equal(t, fmt.Sprintf("updated_%s", key4), key4DocResult["type"])
	assert.Equal(t, "2-1234", key4XattrResult["rev"])

}

func TestGetXattr(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)

	SetUpTestLogging(t, LevelDebug, KeyAll)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	// Doc 1
	key1 := t.Name() + "DocExistsXattrExists"
	val1 := make(map[string]interface{})
	val1["type"] = key1
	xattrName1 := "sync"
	xattrVal1 := make(map[string]interface{})
	xattrVal1["seq"] = float64(1)
	xattrVal1["rev"] = "1-foo"

	// Doc 2 - Tombstone
	key2 := t.Name() + "TombstonedDocXattrExists"
	val2 := make(map[string]interface{})
	val2["type"] = key2
	xattrVal2 := make(map[string]interface{})
	xattrVal2["seq"] = float64(1)
	xattrVal2["rev"] = "1-foo"

	// Doc 3 - To Delete
	key3 := t.Name() + "DeletedDocXattrExists"
	val3 := make(map[string]interface{})
	val3["type"] = key3
	xattrName3 := "sync"
	xattrVal3 := make(map[string]interface{})
	xattrVal3["seq"] = float64(1)
	xattrVal3["rev"] = "1-foo"

	var err error

	// Create w/ XATTR
	cas := uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName1, 0, cas, val1, xattrVal1, nil)
	require.NoError(t, err)

	var response map[string]interface{}

	// Get Xattr From Existing Doc with Existing Xattr
	_, err = dataStore.GetXattr(ctx, key1, xattrName1, &response)
	assert.NoError(t, err)

	assert.Equal(t, xattrVal1["seq"], response["seq"])
	assert.Equal(t, xattrVal1["rev"], response["rev"])

	// Get Xattr From Existing Doc With Non-Existent Xattr -> ErrSubDocBadMulti
	_, err = dataStore.GetXattr(ctx, key1, "non-exist", &response)
	assert.Error(t, err)
	assert.True(t, IsXattrNotFoundError(err))

	// Get Xattr From Non-Existent Doc With Non-Existent Xattr
	_, err = dataStore.GetXattr(ctx, "non-exist", "non-exist", &response)
	assert.Error(t, err)
	assert.True(t, IsDocNotFoundError(err))

	// Get Xattr From Tombstoned Doc With Existing System Xattr (ErrSubDocSuccessDeleted)
	cas, err = dataStore.WriteCasWithXattr(ctx, key2, SyncXattrName, 0, uint64(0), val2, xattrVal2, nil)
	require.NoError(t, err)
	_, err = dataStore.Remove(key2, cas)
	require.NoError(t, err)
	_, err = dataStore.GetXattr(ctx, key2, SyncXattrName, &response)
	assert.NoError(t, err)

	// Get Xattr From Tombstoned Doc With Non-Existent System Xattr -> SubDocMultiPathFailureDeleted
	_, err = dataStore.GetXattr(ctx, key2, "_non-exist", &response)
	assert.Error(t, err)
	assert.True(t, IsXattrNotFoundError(err))

	// Get Xattr and Body From Tombstoned Doc With Non-Existent System Xattr -> SubDocMultiPathFailureDeleted
	var v, xv, userXv map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key2, "_non-exist", "", &v, &xv, &userXv)
	assert.Error(t, err)
	assert.True(t, IsDocNotFoundError(err))

	// Get Xattr From Tombstoned Doc With Deleted User Xattr
	cas, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName3, 0, uint64(0), val3, xattrVal3, nil)
	require.NoError(t, err)
	_, err = dataStore.Remove(key3, cas)
	require.NoError(t, err)
	_, err = dataStore.GetXattr(ctx, key3, xattrName3, &response)
	assert.Error(t, err)
	assert.True(t, IsXattrNotFoundError(err))
}

func TestGetXattrAndBody(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)

	SetUpTestLogging(t, LevelDebug, KeyAll)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	// Doc 1
	key1 := t.Name() + "DocExistsXattrExists"
	val1 := make(map[string]interface{})
	val1["type"] = key1
	xattrName1 := "sync"
	xattrVal1 := make(map[string]interface{})
	xattrVal1["seq"] = float64(1)
	xattrVal1["rev"] = "1-foo"

	// Doc 2 - Tombstone
	key2 := t.Name() + "TombstonedDocXattrExists"
	val2 := make(map[string]interface{})
	val2["type"] = key2
	xattrVal2 := make(map[string]interface{})
	xattrVal2["seq"] = float64(1)
	xattrVal2["rev"] = "1-foo"

	// Doc 3 - To Delete
	key3 := t.Name() + "DeletedDocXattrExists"
	val3 := make(map[string]interface{})
	val3["type"] = key3
	xattrName3 := "sync"
	xattrVal3 := make(map[string]interface{})
	xattrVal3["seq"] = float64(1)
	xattrVal3["rev"] = "1-foo"

	var err error

	// Create w/ XATTR
	cas := uint64(0)
	_, err = dataStore.WriteCasWithXattr(ctx, key1, xattrName1, 0, cas, val1, xattrVal1, nil)
	require.NoError(t, err)

	// Get Xattr From Existing Doc with Existing Xattr
	var v, xv, userXv map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key1, xattrName1, "", &v, &xv, &userXv)
	assert.NoError(t, err)

	assert.Equal(t, xattrVal1["seq"], xv["seq"])
	assert.Equal(t, xattrVal1["rev"], xv["rev"])

	// Get body and Xattr From Existing Doc With Non-Existent Xattr -> returns body only
	_, err = dataStore.GetWithXattr(ctx, key1, "non-exist", "", &v, &xv, &userXv)
	assert.NoError(t, err)
	assert.Equal(t, val1["type"], v["type"])

	// Get Xattr From Non-Existent Doc With Non-Existent Xattr
	_, err = dataStore.GetWithXattr(ctx, "non-exist", "non-exist", "", &v, &xv, &userXv)
	assert.Error(t, err)
	assert.True(t, IsDocNotFoundError(err))

	// Get Xattr From Tombstoned Doc With Existing System Xattr (ErrSubDocSuccessDeleted)
	cas, err = dataStore.WriteCasWithXattr(ctx, key2, SyncXattrName, 0, uint64(0), val2, xattrVal2, nil)
	require.NoError(t, err)
	_, err = dataStore.Remove(key2, cas)
	require.NoError(t, err)
	_, err = dataStore.GetWithXattr(ctx, key2, SyncXattrName, "", &v, &xv, &userXv)
	assert.NoError(t, err)

	// Get Xattr From Tombstoned Doc With Non-Existent System Xattr -> returns not found
	_, err = dataStore.GetWithXattr(ctx, key2, "_non-exist", "", &v, &xv, &userXv)
	assert.Error(t, err)
	assert.True(t, IsDocNotFoundError(err))

	// Get Xattr From Tombstoned Doc With Deleted User Xattr -> returns not found
	cas, err = dataStore.WriteCasWithXattr(ctx, key3, xattrName3, 0, uint64(0), val3, xattrVal3, nil)
	require.NoError(t, err)
	_, err = dataStore.Remove(key3, cas)
	require.NoError(t, err)
	_, err = dataStore.GetWithXattr(ctx, key3, xattrName3, "", &v, &xv, &userXv)
	assert.Error(t, err)
	assert.True(t, IsDocNotFoundError(err))
}

func TestApplyViewQueryOptions(t *testing.T) {

	// View query params map (go-couchbase/walrus style)
	params := map[string]interface{}{
		ViewQueryParamStale:         false,
		ViewQueryParamReduce:        true,
		ViewQueryParamStartKey:      "foo",
		ViewQueryParamEndKey:        "bar",
		ViewQueryParamInclusiveEnd:  true,
		ViewQueryParamLimit:         uint64(1),
		ViewQueryParamIncludeDocs:   true, // Ignored -- see https://forums.couchbase.com/t/do-the-viewquery-options-omit-include-docs-on-purpose/12399
		ViewQueryParamDescending:    true,
		ViewQueryParamGroup:         true,
		ViewQueryParamSkip:          uint64(2),
		ViewQueryParamGroupLevel:    uint64(3),
		ViewQueryParamStartKeyDocId: "baz",
		ViewQueryParamEndKeyDocId:   "blah",
		ViewQueryParamKey:           "hello",
		ViewQueryParamKeys:          []interface{}{"a", "b"},
	}
	ctx := TestCtx(t)
	// Call applyViewQueryOptions (method being tested) which modifies viewQuery according to params
	viewOpts, err := createViewOptions(ctx, params)
	if err != nil {
		t.Fatalf("Error calling applyViewQueryOptions: %v", err)
	}

	// "stale"
	assert.Equal(t, gocb.ViewScanConsistencyRequestPlus, viewOpts.ScanConsistency)

	// "reduce"
	assert.Equal(t, true, viewOpts.Reduce)

	// "startkey"
	assert.Equal(t, "foo", viewOpts.StartKey)

	// "endkey"
	assert.Equal(t, "bar", viewOpts.EndKey)

	// "inclusive_end"
	assert.Equal(t, true, viewOpts.InclusiveEnd)

	// "limit"
	assert.Equal(t, uint32(1), viewOpts.Limit)

	// "descending"
	assert.Equal(t, gocb.ViewOrderingDescending, viewOpts.Order)

	// "group"
	assert.Equal(t, true, viewOpts.Group)

	// "skip"
	assert.Equal(t, uint32(2), viewOpts.Skip)

	// "group_level"
	assert.Equal(t, uint32(3), viewOpts.GroupLevel)

	// "startkey_docid"
	assert.Equal(t, "baz", viewOpts.StartKeyDocID)

	// "endkey_docid"
	assert.Equal(t, "blah", viewOpts.EndKeyDocID)

	// "key"
	assert.Equal(t, "hello", viewOpts.Key)

	// "keys"
	assert.Equal(t,

		[]interface{}{"a", "b"}, viewOpts.Keys)

}

// In certain cases, the params will have strings instead of bools
// https://github.com/couchbase/sync_gateway/issues/2423#issuecomment-296245658
func TestApplyViewQueryOptionsWithStrings(t *testing.T) {

	// View query params map (go-couchbase/walrus style)
	params := map[string]interface{}{
		ViewQueryParamStale:         "false",
		ViewQueryParamReduce:        "true",
		ViewQueryParamStartKey:      "foo",
		ViewQueryParamEndKey:        "bar",
		ViewQueryParamInclusiveEnd:  "true",
		ViewQueryParamLimit:         "1",
		ViewQueryParamIncludeDocs:   "true", // Ignored -- see https://forums.couchbase.com/t/do-the-viewquery-options-omit-include-docs-on-purpose/12399
		ViewQueryParamDescending:    "true",
		ViewQueryParamGroup:         "true",
		ViewQueryParamSkip:          "2",
		ViewQueryParamGroupLevel:    "3",
		ViewQueryParamStartKeyDocId: "baz",
		ViewQueryParamEndKeyDocId:   "blah",
		ViewQueryParamKey:           "hello",
		ViewQueryParamKeys:          []string{"a", "b"},
	}

	_, err := createViewOptions(TestCtx(t), params)
	if err != nil {
		t.Fatalf("Error calling applyViewQueryOptions: %v", err)
	}

	// if it doesn't blow up, test passes

}

// Validate non-bool stale handling
func TestApplyViewQueryStaleOptions(t *testing.T) {

	// View query params map (go-couchbase/walrus style)
	params := map[string]interface{}{
		ViewQueryParamStale: "false",
	}

	ctx := TestCtx(t)
	// if it doesn't blow up, test passes
	if _, err := createViewOptions(ctx, params); err != nil {
		t.Fatalf("Error calling applyViewQueryOptions: %v", err)
	}

	params = map[string]interface{}{
		ViewQueryParamStale: "ok",
	}

	if _, err := createViewOptions(ctx, params); err != nil {
		t.Fatalf("Error calling applyViewQueryOptions: %v", err)
	}

}

func TestCouchbaseServerMaxTTL(t *testing.T) {
	if UnitTestUrlIsWalrus() {
		t.Skip("This test only works against Couchbase Server")
	}

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)

	cbStore, ok := AsCouchbaseBucketStore(bucket)
	require.True(t, ok)
	maxTTL, err := cbStore.MaxTTL(TestCtx(t))
	assert.NoError(t, err, "Unexpected error")
	assert.Equal(t, 0, maxTTL)

}

func TestCouchbaseServerIncorrectLogin(t *testing.T) {
	if UnitTestUrlIsWalrus() {
		t.Skip("This test only works against Couchbase Server")
	}

	ctx := TestCtx(t)
	for _, tls := range []bool{true, false} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			testBucket := GetTestBucket(t)
			defer testBucket.Close(ctx)

			// Override test bucket spec with invalid creds
			fmt.Println("testBucket.BucketSpec: ", testBucket.BucketSpec)
			testBucket.BucketSpec.Auth = TestAuthenticator{
				Username:   "invalid_username",
				Password:   "invalid_password",
				BucketName: testBucket.BucketSpec.BucketName,
			}
			if tls {
				testBucket.BucketSpec.Server = strings.ReplaceAll(testBucket.BucketSpec.Server, "couchbase://", "couchbases://")
			} else {
				testBucket.BucketSpec.Server = strings.ReplaceAll(testBucket.BucketSpec.Server, "couchbases://", "couchbase://")
				SkipInvalidAuthForCouchbaseServer76(t)
			}

			// Attempt to open the bucket again using invalid creds. We should expect an error.
			bucket, err := GetBucket(TestCtx(t), testBucket.BucketSpec)
			assert.Equal(t, ErrAuthError, err)
			assert.Nil(t, bucket)
		})
	}
}

// TestCouchbaseServerIncorrectX509Login tries to open a bucket using an example X509 Cert/Key
// to make sure that we get back a "no access" error.
func TestCouchbaseServerIncorrectX509Login(t *testing.T) {
	t.Skip("Disabled pending CBG-2473")

	ctx := TestCtx(t)
	testBucket := GetTestBucket(t)
	defer testBucket.Close(ctx)

	// Remove existing password-based authentication
	testBucket.BucketSpec.Auth = nil

	// Force use of TLS so we are able to use X509 by stripping protocol and using couchbases://
	if !strings.HasPrefix(testBucket.BucketSpec.Server, "couchbases://") {
		for _, proto := range []string{"http://", "couchbase://", ""} {
			if !strings.HasPrefix(testBucket.BucketSpec.Server, proto) {
				continue
			}
			testBucket.BucketSpec.Server = "couchbases://" + testBucket.BucketSpec.Server[len(proto):]
			testBucket.BucketSpec.Server = strings.TrimSuffix(testBucket.BucketSpec.Server, ":8091")
			break
		}
	}

	// Set CertPath/KeyPath for X509 auth
	certPath, keyPath, x509CleanupFn := tempX509Certs(t)
	testBucket.BucketSpec.Certpath = certPath
	testBucket.BucketSpec.Keypath = keyPath

	// Attempt to open a test bucket with invalid certs
	bucket, err := GetBucket(TestCtx(t), testBucket.BucketSpec)

	// We no longer need the cert files, so go ahead and clean those up now before any assertions stop the test.
	x509CleanupFn()

	// Make sure we get an error when opening the bucket
	require.Error(t, err)

	// And also check it's an access error
	/*
		TODO: CBG-2473
		errCause := pkgerrors.Cause(err)
			kvErr, ok := errCause.(*gocbcore.KvError)
			require.Truef(t, ok, "Expected error type gocbcore.KvError, but got %#v", errCause)
			assert.Equal(t, gocbcore.StatusAccessError, kvErr.Code)
	*/
	assert.Nil(t, bucket)
}

// tempX509Certs creates temporary files for an example X509 cert and key
// which returns the path to those files, and a cleanupFn to remove the files.
func tempX509Certs(t *testing.T) (certPath, keyPath string, cleanupFn func()) {
	tmpDir := os.TempDir()

	// The contents of these certificates was taken directly from Go's tls package examples.
	certFile, err := os.CreateTemp(tmpDir, "x509-cert")
	require.NoError(t, err)
	_, err = certFile.Write([]byte(`-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`))
	require.NoError(t, err)
	_ = certFile.Close()

	keyFile, err := os.CreateTemp(tmpDir, "x509-key")
	require.NoError(t, err)
	_, err = keyFile.Write([]byte(`-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIIrYSSNQFaA2Hwf1duRSxKtLYX5CB04fSeQ6tF1aY/PuoAoGCCqGSM49
AwEHoUQDQgAEPR3tU2Fta9ktY+6P9G0cWO+0kETA6SFs38GecTyudlHz6xvCdz8q
EKTcWGekdmdDPsHloRNtsiCa697B2O9IFA==
-----END EC PRIVATE KEY-----`))
	require.NoError(t, err)
	_ = keyFile.Close()

	certPath = certFile.Name()
	keyPath = keyFile.Name()
	cleanupFn = func() {
		_ = os.Remove(certPath)
		_ = os.Remove(keyPath)
	}

	return certPath, keyPath, cleanupFn
}

func createTombstonedDoc(t *testing.T, dataStore sgbucket.DataStore, key, xattrName string) {

	// Create document with XATTR

	val := make(map[string]interface{})
	val["body_field"] = "1234"

	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-1234"

	ctx := TestCtx(t)
	// Create w/ doc and XATTR
	cas := uint64(0)
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrName, 0, cas, val, xattrVal, nil)
	require.NoError(t, err)

	// Create tombstone revision which deletes doc body but preserves XATTR
	_, mutateErr := dataStore.DeleteBody(ctx, key, xattrName, 0, cas, nil)
	/*
		flags := gocb.SubdocDocFlagAccessDeleted
		_, mutateErr := dataStore.dataStore.MutateInEx(key, flags, gocb.Cas(cas), uint32(0)).
			UpsertEx(xattrName, xattrVal, gocb.SubdocFlagXattr).                                              // Update the xattr
			UpsertEx(SyncXattrName+".cas", "${Mutation.CAS}", gocb.SubdocFlagXattr|gocb.SubdocFlagUseMacros). // Stamp the cas on the xattr
			RemoveEx("", gocb.SubdocFlagNone).                                                                // Delete the document body
			Execute()
	*/
	require.NoError(t, mutateErr)

	// Verify delete of body and XATTR
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	require.NoError(t, err)

	require.Len(t, retrievedVal, 0)
	require.Equal(t, float64(123), retrievedXattr["seq"])

}

func verifyDocAndXattrDeleted(ctx context.Context, store sgbucket.XattrStore, key, xattrName string) bool {
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_, err := store.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)
	return IsDocNotFoundError(err)
}

func verifyDocDeletedXattrExists(ctx context.Context, store sgbucket.XattrStore, key, xattrName string) bool {
	var retrievedVal map[string]interface{}
	var retrievedXattr map[string]interface{}
	_, err := store.GetWithXattr(ctx, key, xattrName, "", &retrievedVal, &retrievedXattr, nil)

	log.Printf("verification for key: %s   body: %s  xattr: %s", key, retrievedVal, retrievedXattr)
	if err != nil || len(retrievedVal) > 0 || len(retrievedXattr) == 0 {
		return false
	}
	return true
}

func TestUpdateXattrWithDeleteBodyAndIsDelete(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)
	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	// Create a document with extended attributes
	key := "DocWithXattrAndIsDelete"
	val := make(map[string]interface{})
	val["type"] = key

	xattrKey := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-EmDC"

	cas := uint64(0)
	// CAS-safe write of the document and it's associated named extended attributes
	cas, err := dataStore.WriteCasWithXattr(ctx, key, xattrKey, 0, cas, val, xattrVal, syncMutateInOpts())
	require.NoError(t, err)

	updatedXattrVal := make(map[string]interface{})
	updatedXattrVal["seq"] = 123
	updatedXattrVal["rev"] = "2-EmDC"

	// Attempt to delete the document body (deleteBody = true); isDelete is true to mark this doc as a tombstone.

	xattrValBytes := MustJSONMarshal(t, updatedXattrVal)
	_, errDelete := dataStore.WriteWithXattr(ctx, key, xattrKey, 0, cas, nil, xattrValBytes, true, true, syncMutateInOpts())
	assert.NoError(t, errDelete, fmt.Sprintf("Unexpected error deleting %s", key))
	assert.True(t, verifyDocDeletedXattrExists(ctx, dataStore, key, xattrKey), fmt.Sprintf("Expected doc %s to be deleted", key))

	var docResult map[string]interface{}
	var xattrResult map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, key, xattrKey, "", &docResult, &xattrResult, nil)
	assert.NoError(t, err)
	assert.Len(t, docResult, 0)
	assert.Equal(t, "2-EmDC", xattrResult["rev"])
	assert.Equal(t, "0x00000000", xattrResult[xattrMacroValueCrc32c])
}

func TestUserXattrGetWithXattr(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)
	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	docKey := t.Name()

	docVal := map[string]interface{}{"val": "docVal"}
	syncXattrVal := map[string]interface{}{"val": "syncVal"}
	userXattrVal := map[string]interface{}{"val": "userXattrVal"}

	err := dataStore.Set(docKey, 0, nil, docVal)
	assert.NoError(t, err)

	_, err = dataStore.WriteUserXattr(docKey, "_sync", syncXattrVal)
	assert.NoError(t, err)

	_, err = dataStore.WriteUserXattr(docKey, "test", userXattrVal)
	assert.NoError(t, err)

	var docValRet, syncXattrValRet, userXattrValRet map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, docKey, SyncXattrName, "test", &docValRet, &syncXattrValRet, &userXattrValRet)
	assert.NoError(t, err)
	assert.Equal(t, docVal, docValRet)
	assert.Equal(t, syncXattrVal, syncXattrValRet)
	assert.Equal(t, userXattrVal, userXattrValRet)
}

func TestUserXattrGetWithXattrNil(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)
	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	docKey := t.Name()

	docVal := map[string]interface{}{"val": "docVal"}
	syncXattrVal := map[string]interface{}{"val": "syncVal"}

	err := dataStore.Set(docKey, 0, nil, docVal)
	assert.NoError(t, err)

	_, err = dataStore.WriteUserXattr(docKey, "_sync", syncXattrVal)
	assert.NoError(t, err)

	var docValRet, syncXattrValRet, userXattrValRet map[string]interface{}
	_, err = dataStore.GetWithXattr(ctx, docKey, SyncXattrName, "test", &docValRet, &syncXattrValRet, &userXattrValRet)
	assert.NoError(t, err)
	assert.Equal(t, docVal, docValRet)
	assert.Equal(t, syncXattrVal, syncXattrValRet)
}

func TestInsertTombstoneWithXattr(t *testing.T) {
	SkipXattrTestsIfNotEnabled(t)
	SetUpTestLogging(t, LevelDebug, KeyCRUD)

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	// Create a document with extended attributes
	key := "InsertedTombstoneDoc"
	val := make(map[string]interface{})
	val["type"] = key

	xattrKey := SyncXattrName
	xattrVal := make(map[string]interface{})
	xattrVal["seq"] = 123
	xattrVal["rev"] = "1-EmDC"

	cas := uint64(0)
	// Attempt to delete the document body (deleteBody = true); isDelete is true to mark this doc as a tombstone.
	xattrValBytes := MustJSONMarshal(t, xattrVal)
	_, errDelete := dataStore.WriteWithXattr(ctx, key, xattrKey, 0, cas, nil, xattrValBytes, true, false, syncMutateInOpts())
	assert.NoError(t, errDelete, fmt.Sprintf("Unexpected error deleting %s", key))
	assert.True(t, verifyDocDeletedXattrExists(ctx, dataStore, key, xattrKey), fmt.Sprintf("Expected doc %s to be deleted", key))

	var docResult map[string]interface{}
	var xattrResult map[string]interface{}
	_, err := dataStore.GetWithXattr(ctx, key, xattrKey, "", &docResult, &xattrResult, nil)
	assert.NoError(t, err)
	assert.Len(t, docResult, 0)
	assert.Equal(t, "1-EmDC", xattrResult["rev"])
	assert.Equal(t, "0x00000000", xattrResult[xattrMacroValueCrc32c])
}

// TestRawBackwardCompatibilityFromJSON ensures that bucket implementation handles the case
// where legacy SG versions set incorrect data types:
//   - write as JSON, read as binary, (re-)write as binary
func TestRawBackwardCompatibilityFromJSON(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	val := []byte(`{"foo":"bar"}`)
	updatedVal := []byte(`{"foo":"bars"}`)

	// Write as JSON
	setErr := dataStore.Set(key, 0, nil, val)
	assert.NoError(t, setErr)

	// Read as binary
	rv, _, getRawErr := dataStore.GetRaw(key)
	assert.NoError(t, getRawErr)
	if string(rv) != string(val) {
		t.Errorf("%v != %v", string(rv), string(val))
	}

	// Write as binary
	setRawErr := dataStore.SetRaw(key, 0, nil, updatedVal)
	assert.NoError(t, setRawErr)
}

// TestRawBackwardCompatibilityFromBinary ensures that bucket implementation handles the case
// where legacy SG versions set incorrect data types:
//   - write as binary, read as raw JSON, rewrite as raw JSON
func TestRawBackwardCompatibilityFromBinary(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	val := []byte(`{{"foo":"bar"}`)
	updatedVal := []byte(`{"foo":"bars"}`)

	// Write as binary
	require.NoError(t, dataStore.SetRaw(key, 0, nil, val))

	// Read as raw JSON
	var rv []byte
	_, getErr := dataStore.Get(key, &rv)
	require.NoError(t, getErr)
	require.Equal(t, string(val), string(rv))

	// Write as raw JSON
	setErr := dataStore.Set(key, 0, nil, updatedVal)
	assert.NoError(t, setErr)
}

func TestGetExpiry(t *testing.T) {

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	key := t.Name()
	val := make(map[string]interface{}, 0)
	val["foo"] = "bar"

	expiryValue := uint32(time.Now().Add(1 * time.Minute).Unix())
	err := dataStore.Set(key, expiryValue, nil, val)
	assert.NoError(t, err, "Error calling Set()")

	expiry, expiryErr := dataStore.GetExpiry(ctx, key)
	assert.NoError(t, expiryErr)

	// gocb v2 expiry does an expiry-to-duration conversion which results in non-exact equality,
	// so check whether it's within 30s
	assert.Less(t, DiffUint32(expiryValue, expiry), uint32(30))
	log.Printf("expiryValue: %d", expiryValue)
	log.Printf("expiry: %d", expiry)

	require.NoError(t, dataStore.Delete(key))

	// ensure expiry retrieval on tombstone doesn't return error
	tombstoneExpiry, tombstoneExpiryErr := dataStore.GetExpiry(ctx, key)
	assert.NoError(t, tombstoneExpiryErr)
	log.Printf("tombstoneExpiry: %d", tombstoneExpiry)

	// ensure expiry retrieval on non-existent doc returns key not found
	_, nonExistentExpiryErr := dataStore.GetExpiry(ctx, "nonExistentKey")
	assert.Error(t, nonExistentExpiryErr)
	assert.True(t, IsKeyNotFoundError(dataStore, nonExistentExpiryErr))
}

func TestGetStatsVbSeqNo(t *testing.T) {

	if UnitTestUrlIsWalrus() {
		t.Skip("Walrus doesn't support stats-vbseqno")
	}

	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()

	cbstore, ok := AsCouchbaseBucketStore(bucket)
	assert.True(t, ok)

	maxVbNo, err := cbstore.GetMaxVbno()
	assert.NoError(t, err)

	// Write docs to increment vbseq in at least one vbucket
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("doc%d", i)
		value := map[string]interface{}{"k": "v"}
		ok, err := dataStore.Add(key, 0, value)
		require.NoError(t, err)
		assert.True(t, ok)
	}

	uuids, highSeqNos, statsErr := cbstore.GetStatsVbSeqno(maxVbNo, false)
	assert.NoError(t, statsErr)

	assert.NotNil(t, uuids)
	assert.NotNil(t, highSeqNos)
	assert.Greater(t, len(uuids), 0)
	assert.Greater(t, len(highSeqNos), 0)
}

// Confirm that GoCBv2 preserveExpiry option works correctly for bucket Set function
func TestUpsertOptionPreserveExpiry(t *testing.T) {
	ctx := TestCtx(t)
	bucket := GetTestBucket(t)
	defer bucket.Close(ctx)
	dataStore := bucket.GetSingleDataStore()
	if !bucket.IsSupported(sgbucket.BucketStoreFeaturePreserveExpiry) {
		t.Skip("Preserve expiry is not supported with this CBS version. Skipping test...")
	}
	SetUpTestLogging(t, LevelInfo, KeyAll)

	testCases := []struct {
		name          string
		upsertOptions *sgbucket.UpsertOptions
		expectMatch   bool
	}{
		{
			name:          "Expect matching expiry - preserveExpiry",
			upsertOptions: &sgbucket.UpsertOptions{PreserveExpiry: true},
			expectMatch:   true,
		},
		{
			name:          "Expect updated expiry - false preserveExpiry",
			upsertOptions: &sgbucket.UpsertOptions{PreserveExpiry: false},
			expectMatch:   false,
		},
		{
			name:          "Expect updated expiry - nil upsert options",
			upsertOptions: nil,
			expectMatch:   false,
		},
	}

	for i, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			key := fmt.Sprintf("test%d", i)
			val := make(map[string]interface{}, 0)
			val["foo"] = "bar"

			var rVal map[string]interface{}
			_, err := dataStore.Get(key, &rVal)
			assert.Error(t, err, "Key should not exist yet, expected error but got nil")

			err = dataStore.Set(key, DurationToCbsExpiry(time.Hour*24), nil, val)
			assert.NoError(t, err, "Error calling Set()")

			ctx := TestCtx(t)
			beforeExp, err := dataStore.GetExpiry(ctx, key)
			require.NoError(t, err)
			require.NotEqual(t, 0, beforeExp)

			val["foo"] = "baz"
			err = dataStore.Set(key, 0, test.upsertOptions, val)
			assert.NoError(t, err, "Error calling Set()")

			afterExp, err := dataStore.GetExpiry(ctx, key)
			assert.NoError(t, err)
			if test.expectMatch {
				assert.Equal(t, beforeExp, afterExp) // Make sure both expiry timestamps match
			} else {
				assert.NotEqual(t, beforeExp, afterExp) // Make sure both expiry timestamps do not match
			}

			require.NoError(t, dataStore.Delete(key))
		})
	}
}

// TestMobileSystemCollectionCRUD ensures that if the mobile system collection exists, Sync Gateway is able to perform CRUD on a document in the mobile system collection.
func TestMobileSystemCollectionCRUD(t *testing.T) {
	b := getTestBucket(t, false)
	defer b.Close(TestCtx(t))

	if !b.IsSupported(sgbucket.BucketStoreFeatureSystemCollections) {
		WarnfCtx(TestCtx(t), "Non-fatal test failure: Configured Couchbase Server does not support system collections")
		t.Skipf("Non-fatal test failure: Configured Couchbase Server does not support system collections")
	}
	var docID = t.Name()

	ds, err := b.NamedDataStore(ScopeAndCollectionName{Scope: SystemScope, Collection: SystemCollectionMobile})
	require.NoError(t, err)

	_, err = ds.Exists(t.Name())
	require.NoErrorf(t, err, "Expected %s.%s to exist on server capable of system collections", SystemScope, SystemCollectionMobile)

	field1Key := "field1"
	field1Val := true
	body := map[string]interface{}{field1Key: true}
	created, err := ds.Add(docID, 0, body)
	require.NoError(t, err)
	require.True(t, created)

	var val map[string]interface{}
	casGet, err := ds.Get(docID, &val)
	require.NoError(t, err)
	assert.Equal(t, body, val)

	newField := KVPair{"field2", "val"}
	casUpdate, err := ds.Update(docID, 0, func(current []byte) (updated []byte, expiry *uint32, delete bool, err error) {
		newBody, err := InjectJSONProperties(current, newField)
		return newBody, nil, false, err
	})
	require.NoError(t, err)
	require.Greater(t, casUpdate, casGet)

	val = nil
	casGet, err = ds.Get(docID, &val)
	require.NoError(t, err)
	assert.Equal(t, casUpdate, casGet)
	field1ValGet, ok := val[field1Key]
	require.True(t, ok)
	assert.Equal(t, field1Val, field1ValGet)
	newFieldValGet, ok := val[newField.Key]
	require.True(t, ok)
	assert.Equal(t, newField.Val, newFieldValGet)

	err = ds.Delete(docID)
	require.NoError(t, err)
}

// Used to test standard sync mutateInOpts from the base package
func syncMutateInOpts() *sgbucket.MutateInOptions {
	return &sgbucket.MutateInOptions{
		MacroExpansion: []sgbucket.MacroExpansionSpec{
			sgbucket.NewMacroExpansionSpec(xattrCasPath(SyncXattrName), sgbucket.MacroCas),
			sgbucket.NewMacroExpansionSpec(xattrCrc32cPath(SyncXattrName), sgbucket.MacroCrc32c),
		},
	}
}
