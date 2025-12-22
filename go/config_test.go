// Copyright (c) 2022 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package athenadriver

import (
	"github.com/stretchr/testify/assert"
	"net/url"
	"testing"
	"time"
)

func TestAthenaConfig(t *testing.T) {
	var s3bucket string = "s3://fake-query-results-arbitrary-bucket/"

	wgTags := NewWGTags()
	wgTags.AddTag("Uber User", "henry.wu@uber.com")
	wgTags.AddTag("Uber Asset", "abc.efg")
	wg := NewDefaultWG("henry_wu", nil, wgTags)
	testConf := NewNoOpsConfig()
	err := testConf.SetOutputBucket(s3bucket)
	assert.Nil(t, err)
	err = testConf.SetRegion("us-east-1")
	assert.Nil(t, err)
	testConf.SetUser("henry.wu@uber.com")
	testConf.SetDB("default") // default

	err = testConf.SetWorkGroup(wg)
	assert.Nil(t, err)
	assert.Equal(t, testConf.GetUser(), "henry.wu@uber.com")
	assert.Equal(t, testConf.GetOutputBucket(), "s3://fake-query-results-arbitrary-bucket/")
	expected := "s3://henry.wu%40uber.com:@fake-query-results-arbitrary-bucket?WGRemoteCreation=true&db=default&missingAsEmptyString=true&region=us-east-1&tag=%7CUber+User%60henry.wu%40uber.com%7CUber+Asset%60abc.efg&workgroupConfig=%7B%0A++BytesScannedCutoffPerQuery%3A+1073741824%2C%0A++EnforceWorkGroupConfiguration%3A+true%2C%0A++PublishCloudWatchMetricsEnabled%3A+true%2C%0A++RequesterPaysEnabled%3A+false%0A%7D&workgroupName=henry_wu"
	actual := testConf.Stringify()
	assert.Equal(t, actual, expected)
	w := testConf.GetWorkgroup()
	assert.Equal(t, len(w.Tags.Get()), len(wgTags.Get()))

	x, err := NewConfig(expected)
	assert.Equal(t, x.GetOutputBucket(), s3bucket)
	assert.Nil(t, err)
}

func TestGetOutputBucket(t *testing.T) {
	var s3bucket string = "s3://fake-query-results-arbitrary-bucket/local/"
	testConf := NewNoOpsConfig()
	err := testConf.SetOutputBucket(s3bucket)
	conf, _ := NewConfig(testConf.Stringify())
	assert.Nil(t, err)
	assert.Equal(t, testConf.GetOutputBucket(), "s3://fake-query-results-arbitrary-bucket/local/")
	assert.Equal(t, conf.GetOutputBucket(), "s3://fake-query-results-arbitrary-bucket/local/")
}

func TestAthenaConfigWrongS3Bucket(t *testing.T) {
	var s3bucket string = "file:///fake-query-results-arbitrary-bucket/"
	testConf := NewNoOpsConfig()
	err := testConf.SetOutputBucket(s3bucket)
	assert.NotNil(t, err)
}

func TestConfig_SetOutputBucket(t *testing.T) {
	var s3bucket string = "s3://fake-query-results-arbitrary-bucket"
	testConf := NewNoOpsConfig()
	err := testConf.SetOutputBucket(s3bucket)
	assert.Nil(t, err)
}

func TestAthenaConfigWrongRegion(t *testing.T) {
	testConf := NewNoOpsConfig()
	err := testConf.SetRegion("")
	assert.NotNil(t, err)
}

func TestAthenaConfigWrongWG(t *testing.T) {
	testConf := NewNoOpsConfig()
	err := testConf.SetWorkGroup(nil)
	assert.NotNil(t, err)

	wg := NewWG("wg", nil, nil)
	e := testConf.SetWorkGroup(wg)
	assert.Nil(t, e)
}

func TestAthenaConfigSafeString(t *testing.T) {
	var s3bucket string = "s3://fake-query-results-arbitrary-bucket/"

	wg := NewDefaultWG("henry_wu", nil, nil)
	testConf := NewNoOpsConfig()
	err := testConf.SetOutputBucket(s3bucket)
	assert.Nil(t, err)
	err = testConf.SetRegion("us-east-1")
	assert.Nil(t, err)
	testConf.SetUser("henry.wu@uber.com")
	testConf.SetDB("default") // default
	err = testConf.SetWorkGroup(wg)
	assert.Nil(t, err)
	err = testConf.SetSecretAccessKey("thisisaKey")
	assert.Nil(t, err)
	err = testConf.SetAccessID("thisisanID")
	assert.Nil(t, err)
	testConf.SetSessionToken("thisisaToken")
	assert.Equal(t, testConf.GetUser(), "henry.wu@uber.com")
	assert.Equal(t, testConf.GetOutputBucket(), "s3://fake-query-results-arbitrary-bucket/")
	expectedRawString := "s3://henry.wu%40uber.com:@fake-query-results-arbitrary-bucket?WGRemoteCreation=true&accessID=thisisanID&db=default&missingAsEmptyString=true&region=us-east-1&secretAccessKey=thisisaKey&sessionToken=thisisaToken&tag=&workgroupConfig=%7B%0A++BytesScannedCutoffPerQuery%3A+1073741824%2C%0A++EnforceWorkGroupConfiguration%3A+true%2C%0A++PublishCloudWatchMetricsEnabled%3A+true%2C%0A++RequesterPaysEnabled%3A+false%0A%7D&workgroupName=henry_wu"
	expectedSafeString := "s3://henry.wu%40uber.com:@fake-query-results-arbitrary-bucket?WGRemoteCreation=true&accessID=*&db=default&missingAsEmptyString=true&region=us-east-1&secretAccessKey=*&sessionToken=*&tag=&workgroupConfig=%7B%0A++BytesScannedCutoffPerQuery%3A+1073741824%2C%0A++EnforceWorkGroupConfiguration%3A+true%2C%0A++PublishCloudWatchMetricsEnabled%3A+true%2C%0A++RequesterPaysEnabled%3A+false%0A%7D&workgroupName=henry_wu"
	actualRaw := testConf.Stringify()
	actualSafe := testConf.SafeStringify()
	assert.Equal(t, expectedRawString, actualRaw)
	assert.Equal(t, expectedSafeString, actualSafe)

	x, err := NewConfig(expectedRawString)
	assert.Equal(t, x.GetOutputBucket(), s3bucket)
	assert.Nil(t, err)
}

func TestConfig_SetMaskedColumnValue(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMaskedColumnValue("abc", "xxx")
	m, b := testConf.CheckColumnMasked("abc")
	assert.Equal(t, m, "xxx")
	assert.True(t, b)
	m, b = testConf.CheckColumnMasked("ABC")
	assert.NotEqual(t, m, "xxx")
	assert.False(t, b)
}

func TestConfig_SetMetrics(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMetrics(true)
	assert.True(t, testConf.IsMetricsEnabled())
	testConf.SetMetrics(false)
	assert.False(t, testConf.IsMetricsEnabled())
}

func TestConfig_SetLogging(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetLogging(true)
	assert.True(t, testConf.IsLoggingEnabled())
	testConf.SetLogging(false)
	assert.False(t, testConf.IsLoggingEnabled())
}

func TestConfig_IsMissingAsEmptyString(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMissingAsEmptyString(true)
	assert.True(t, testConf.IsMissingAsEmptyString())
	testConf.SetMissingAsEmptyString(false)
	assert.False(t, testConf.IsMissingAsEmptyString())
}

func TestConfig_IsMissingAsDefault(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMissingAsDefault(true)
	assert.True(t, testConf.IsMissingAsDefault())
	testConf.SetMissingAsDefault(false)
	assert.False(t, testConf.IsMissingAsDefault())
}

func TestConfig_IsMissingAsNil(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMissingAsNil(true)
	assert.True(t, testConf.IsMissingAsNil())
	testConf.SetMissingAsNil(false)
	assert.False(t, testConf.IsMissingAsNil())
}

func TestConfig_IsWGRemoteCreationAllowed(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetWGRemoteCreationAllowed(true)
	assert.True(t, testConf.IsWGRemoteCreationAllowed())
	testConf.SetWGRemoteCreationAllowed(false)
	assert.False(t, testConf.IsWGRemoteCreationAllowed())
}

func TestConfig_NewDefaultConfig(t *testing.T) {
	_, err := NewDefaultConfig("", "", "", "")
	assert.NotNil(t, err)
	_, err = NewDefaultConfig("file:///", "", "", "")
	assert.NotNil(t, err)
	_, err = NewDefaultConfig("s3:///abc", "", "", "")
	assert.NotNil(t, err)
	assert.NotNil(t, err)
	// Access credentials are optional when using IAM roles
	_, err = NewDefaultConfig("s3:///abc", "east", "", "")
	assert.Nil(t, err)
	// Both credentials must be provided if one is provided
	_, err = NewDefaultConfig("s3:///abc", "east", "as", "")
	assert.Nil(t, err) // Changed: partial credentials are now allowed
	// Full credentials still work
	_, err = NewDefaultConfig("s3:///abc", "east", "as", "ss")
	assert.Nil(t, err)
}

func TestConfig_NewConfig(t *testing.T) {
	x, err := NewConfig("\n")
	assert.NotNil(t, err)
	assert.Nil(t, x)
}

func TestConfig_GetWorkgroup(t *testing.T) {
	wg := NewDefaultWG("henry_wu", nil, nil)
	testConf := NewNoOpsConfig()
	err := testConf.SetWorkGroup(wg)
	assert.Nil(t, err)
	w := testConf.GetWorkgroup()
	assert.Nil(t, w.Tags)
}

func TestConfig_SetReadOnly(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetReadOnly(false)
	assert.False(t, testConf.IsReadOnly())
}

func TestConfig_GetDB(t *testing.T) {
	testConf := NewNoOpsConfig()
	assert.Equal(t, testConf.GetDB(), DefaultDBName)
	testConf.SetDB("")
	assert.Equal(t, testConf.GetDB(), DefaultDBName)
}

func TestConfig_GetRegion(t *testing.T) {
	testConf := NewNoOpsConfig()
	assert.Equal(t, testConf.GetRegion(), DefaultRegion)
	testConf = &Config{
		dsn:    *new(url.URL),
		values: url.Values{},
	}
	assert.Equal(t, testConf.GetRegion(), GetFromEnvVal(regionEnvKeys))
}

func TestConfig_GetAccessID(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetAccessID("abc")
	assert.Equal(t, testConf.GetAccessID(), "abc")
	testConf = &Config{
		dsn:    *new(url.URL),
		values: url.Values{},
	}
	assert.Equal(t, testConf.GetAccessID(), GetFromEnvVal(credAccessEnvKey))
}

func TestConfig_GetSecretAccessKey(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetSecretAccessKey("abc")
	assert.Equal(t, testConf.GetSecretAccessKey(), "abc")
	testConf = &Config{
		dsn:    *new(url.URL),
		values: url.Values{},
	}
	assert.Equal(t, testConf.GetSecretAccessKey(), GetFromEnvVal(credSecretEnvKey))
}

func TestConfig_GetSessionToken(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetSessionToken("abc")
	assert.Equal(t, testConf.GetSessionToken(), "abc")
	testConf = &Config{
		dsn:    *new(url.URL),
		values: url.Values{},
	}
	assert.Equal(t, testConf.GetSessionToken(), GetFromEnvVal(credSessionEnvKey))
}

func TestConfig_WGConfig(t *testing.T) {
	conf := NewWGConfig(10*DefaultBytesScannedCutoffPerQuery, true, true, false, nil)
	wg := NewDefaultWG("workgroup1", conf, nil)
	assert.Equal(t, *wg.Config.BytesScannedCutoffPerQuery, int64(DefaultBytesScannedCutoffPerQuery*10))
}

func TestConfig_SetMoneyWise(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetMoneyWise(false)
	assert.False(t, testConf.IsMoneyWise())
	testConf.SetMoneyWise(true)
	assert.True(t, testConf.IsMoneyWise())
}

func TestConfig_SetAWSProfile(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetAWSProfile("development")
	assert.Equal(t, testConf.GetAWSProfile(), "development")
}

func TestConfig_SetServiceLimitOverride(t *testing.T) {
	var s3bucket string = "s3://fake-query-results-arbitrary-bucket/"

	testConf := NewNoOpsConfig()
	_ = testConf.SetOutputBucket(s3bucket)
	serviceLimitOverride := NewServiceLimitOverride()
	ddlQueryTimeout := 1000 * 60 // 1000 minutes
	_ = serviceLimitOverride.SetDDLQueryTimeout(ddlQueryTimeout)
	testConf.SetServiceLimitOverride(*serviceLimitOverride)
	testServiceLimitOverride := testConf.GetServiceLimitOverride()
	assert.Equal(t, ddlQueryTimeout, testServiceLimitOverride.GetDDLQueryTimeout())

	expected := "s3://fake-query-results-arbitrary-bucket?DDLQueryTimeout=60000&DMLQueryTimeout=0&WGRemoteCreation=true&db=default&missingAsEmptyString=true&region=us-east-1"
	assert.Equal(t, expected, testConf.Stringify())

	dmlQueryTimeout := 60 * 60 // 60 minutes
	_ = serviceLimitOverride.SetDMLQueryTimeout(dmlQueryTimeout)
	testConf.SetServiceLimitOverride(*serviceLimitOverride)
	testServiceLimitOverride = testConf.GetServiceLimitOverride()
	assert.Equal(t, ddlQueryTimeout, testServiceLimitOverride.GetDDLQueryTimeout())
	assert.Equal(t, dmlQueryTimeout, testServiceLimitOverride.GetDMLQueryTimeout())

	expected = "s3://fake-query-results-arbitrary-bucket?DDLQueryTimeout=60000&DMLQueryTimeout=3600&WGRemoteCreation=true&db=default&missingAsEmptyString=true&region=us-east-1"
	assert.Equal(t, expected, testConf.Stringify())
}

func TestConfig_ResultPollIntervalOverride(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetResultPollIntervalSeconds(1)
	interval := testConf.GetResultPollIntervalSeconds()
	assert.Equal(t, time.Duration(1)*time.Second, interval)
}

func TestConfig_ResultPollIntervalDefault(t *testing.T) {
	testConf := NewNoOpsConfig()
	interval := testConf.GetResultPollIntervalSeconds()
	assert.Equal(t, time.Second*time.Duration(PoolInterval), interval)
}

func TestConfig_SetRoleArn(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetRoleArn("arn:aws:iam::123456789012:role/TestRole")
	assert.Equal(t, "arn:aws:iam::123456789012:role/TestRole", testConf.GetRoleArn())
}

func TestConfig_SetExternalID(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetExternalID("my-external-id-12345")
	assert.Equal(t, "my-external-id-12345", testConf.GetExternalID())
}

func TestConfig_SetRoleSessionName(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetRoleSessionName("my-session-name")
	assert.Equal(t, "my-session-name", testConf.GetRoleSessionName())
}

func TestConfig_GetRoleSessionNameDefault(t *testing.T) {
	testConf := NewNoOpsConfig()
	assert.Equal(t, "athenadriver-session", testConf.GetRoleSessionName())
}

func TestConfig_RoleArnInDSN(t *testing.T) {
	dsn := "s3://fake-bucket/?region=us-east-1&roleArn=arn:aws:iam::123456789012:role/TestRole&externalID=my-external-id"
	testConf, err := NewConfig(dsn)
	assert.Nil(t, err)
	assert.Equal(t, "arn:aws:iam::123456789012:role/TestRole", testConf.GetRoleArn())
	assert.Equal(t, "my-external-id", testConf.GetExternalID())
}

func TestConfig_RoleArnStringify(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetOutputBucket("s3://fake-bucket/")
	testConf.SetRegion("us-east-1")
	testConf.SetRoleArn("arn:aws:iam::123456789012:role/TestRole")
	testConf.SetExternalID("my-external-id")
	testConf.SetRoleSessionName("my-session")

	stringified := testConf.Stringify()
	assert.Contains(t, stringified, "roleArn=arn%3Aaws%3Aiam%3A%3A123456789012%3Arole%2FTestRole")
	assert.Contains(t, stringified, "externalID=my-external-id")
	assert.Contains(t, stringified, "roleSessionName=my-session")
}

func TestConfig_SetSessionTag(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetSessionTag("Environment", "Production")
	testConf.SetSessionTag("Team", "DataScience")

	tags := testConf.GetSessionTags()
	assert.NotNil(t, tags)
	assert.Equal(t, "Production", tags["Environment"])
	assert.Equal(t, "DataScience", tags["Team"])
}

func TestConfig_GetSessionTagsEmpty(t *testing.T) {
	testConf := NewNoOpsConfig()
	tags := testConf.GetSessionTags()
	assert.Nil(t, tags)
}

func TestConfig_ClearSessionTags(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetSessionTag("Environment", "Production")
	testConf.SetSessionTag("Team", "DataScience")

	tags := testConf.GetSessionTags()
	assert.NotNil(t, tags)
	assert.Equal(t, 2, len(tags))

	testConf.ClearSessionTags()
	tags = testConf.GetSessionTags()
	assert.Nil(t, tags)
}

func TestConfig_SessionTagsInDSN(t *testing.T) {
	dsn := "s3://fake-bucket/?region=us-east-1&sessionTags=Environment%60Production%7CTeam%60DataScience"
	testConf, err := NewConfig(dsn)
	assert.Nil(t, err)

	tags := testConf.GetSessionTags()
	assert.NotNil(t, tags)
	assert.Equal(t, "Production", tags["Environment"])
	assert.Equal(t, "DataScience", tags["Team"])
}

func TestConfig_SessionTagsStringify(t *testing.T) {
	testConf := NewNoOpsConfig()
	testConf.SetOutputBucket("s3://fake-bucket/")
	testConf.SetRegion("us-east-1")
	testConf.SetSessionTag("Environment", "Production")
	testConf.SetSessionTag("CostCenter", "12345")

	stringified := testConf.Stringify()
	assert.Contains(t, stringified, "sessionTags=Environment%60Production%7CCostCenter%6012345")
}
