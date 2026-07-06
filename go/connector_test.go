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
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally/v4"
	"go.uber.org/zap"
)

func TestSQLConnector(t *testing.T) {
	testConf := NewNoOpsConfig()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	conn, err := connector.Connect(context.Background())
	assert.Nil(t, err)
	prepStatement, err := conn.Prepare("select 123")
	assert.Nil(t, err)
	assert.NotNil(t, prepStatement)
	assert.Nil(t, conn.Close())
	transaction, err := conn.Begin()
	assert.Nil(t, transaction)
	assert.Equal(t, err.Error(), "Athena doesn't support transaction statements")
}

func TestSQLConnector_Connect(t *testing.T) {
	testConf := NewNoOpsConfig()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, LoggerKey, logger)
	ctx = context.WithValue(ctx, MetricsKey, tally.NoopScope)
	conn, err := connector.Connect(ctx)
	assert.Nil(t, err)
	prepStatement, err := conn.Prepare("select 123")
	assert.Nil(t, err)
	assert.NotNil(t, prepStatement)
	assert.Nil(t, conn.Close())
	transaction, err := conn.Begin()
	assert.Nil(t, transaction)
	assert.Equal(t, err.Error(), "Athena doesn't support transaction statements")
}

func TestSQLConnector_Connect_NewSessionFail(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_SDK_LOAD_CONFIG", "1")
	// A nonexistent CA bundle makes config.LoadDefaultConfig fail eagerly. (In
	// SDK v1 the equivalent trigger was an invalid AWS_STS_REGIONAL_ENDPOINTS,
	// which v2 no longer validates at load time.)
	os.Setenv("AWS_CA_BUNDLE", "/nonexistent/athenadriver-ca-bundle.pem")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_SDK_LOAD_CONFIG")
	os.Unsetenv("AWS_CA_BUNDLE")
	assert.NotNil(t, err)
	assert.Nil(t, conn)
}

func TestSQLConnector_Connect_NewSession_AWS_SDK_LOAD_CONFIG_true(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_SDK_LOAD_CONFIG", "true")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_SDK_LOAD_CONFIG")
	os.Unsetenv("AWS_STS_REGIONAL_ENDPOINTS")
	assert.Nil(t, err)
	assert.NotNil(t, conn)
}

func TestSQLConnector_Connect_NewSession_AWS_SDK_LOAD_CONFIG_true_AWSProfile_Set(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	testConf.SetAWSProfile("hello-profile")
	os.Setenv("AWS_SDK_LOAD_CONFIG", "true")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_SDK_LOAD_CONFIG")
	os.Unsetenv("AWS_STS_REGIONAL_ENDPOINTS")
	// SDK v2 validates the shared-config profile eagerly, so selecting a profile
	// that does not exist fails fast at Connect. (SDK v1 deferred this and
	// returned a session, only failing later on first credential use.)
	assert.NotNil(t, err)
	assert.Nil(t, conn)
}

func TestSQLConnector_Connect_NewSession_AWS_SDK_LOAD_CONFIG_false(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_SDK_LOAD_CONFIG", "0")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_SDK_LOAD_CONFIG")
	os.Unsetenv("AWS_STS_REGIONAL_ENDPOINTS")
	assert.Nil(t, err)
	assert.NotNil(t, conn)
}

func TestSQLConnector_Connect_NewSession_Credentials(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	_ = testConf.SetAccessID("testid")
	_ = testConf.SetSecretAccessKey("testkey")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	conn, err := connector.Connect(context.Background())

	assert.Nil(t, err)
	assert.NotNil(t, conn)
}

// TestSQLConnector_Connect_Credentials_IgnoresAmbientProfile is a regression
// test for the SDK v2 migration: an explicit static-credential connection must
// not fail merely because AWS_PROFILE points at a profile that does not exist.
// Building the config via LoadDefaultConfig parsed shared config first and
// returned SharedConfigProfileNotExistError before honoring the supplied
// credentials; building aws.Config directly avoids that.
func TestSQLConnector_Connect_Credentials_IgnoresAmbientProfile(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	_ = testConf.SetAccessID("testid")
	_ = testConf.SetSecretAccessKey("testkey")
	os.Setenv("AWS_PROFILE", "athenadriver-nonexistent-profile-regression")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_PROFILE")
	assert.Nil(t, err)
	assert.NotNil(t, conn)
}

// TestSQLConnector_Connect_AssumeRole_IgnoresAmbientProfile mirrors the above
// for the assume-role path when explicit base credentials are supplied.
func TestSQLConnector_Connect_AssumeRole_IgnoresAmbientProfile(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	_ = testConf.SetAccessID("testid")
	_ = testConf.SetSecretAccessKey("testkey")
	testConf.SetRoleArn("arn:aws:iam::123456789012:role/athenadriver-regression")
	os.Setenv("AWS_PROFILE", "athenadriver-nonexistent-profile-regression")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	conn, err := connector.Connect(context.Background())

	os.Unsetenv("AWS_PROFILE")
	assert.Nil(t, err)
	assert.NotNil(t, conn)
}

func TestSQLConnector_Driver(t *testing.T) {
	testConf := NewNoOpsConfig()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	assert.NotNil(t, connector.Driver())
}
