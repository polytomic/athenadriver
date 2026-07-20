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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally/v4"
	"go.uber.org/zap"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
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
// LoadDefaultConfig would parse shared config first and fail before honoring
// the supplied credentials; the explicit-credential path avoids it entirely.
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

// writeTestCABundle writes a self-signed certificate to a temp file and
// returns its path, for use as an AWS_CA_BUNDLE value. The SDK rejects a
// bundle it cannot parse, so this must be a real PEM.
func writeTestCABundle(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.Nil(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "athenadriver-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	assert.Nil(t, err)

	path := filepath.Join(t.TempDir(), "ca-bundle.pem")
	assert.Nil(t, os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	return path
}

// TestSQLConnector_loadConfigWithCredentials_HonorsCABundle is a regression
// test for the SDK v2 migration: explicit credentials must not cost us the
// SDK's resolved transport settings. SDK v1's session.NewSession applied
// AWS_CA_BUNDLE on the static-credential and assume-role paths regardless of
// AWS_SDK_LOAD_CONFIG, so constructing aws.Config directly from region and
// credentials alone silently broke custom-CA environments: Connect succeeded
// but every Athena/S3 request failed TLS validation.
func TestSQLConnector_loadConfigWithCredentials_HonorsCABundle(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_CA_BUNDLE", writeTestCABundle(t))
	defer os.Unsetenv("AWS_CA_BUNDLE")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	cfg, err := connector.loadConfigWithCredentials(context.Background(),
		credentials.NewStaticCredentialsProvider("testid", "testkey", ""))

	assert.Nil(t, err)
	assert.Equal(t, "ap-southeast-1", cfg.Region)
	// The SDK signals a custom bundle by installing an HTTP client whose
	// transport carries a non-default root CA pool.
	client, ok := cfg.HTTPClient.(*awshttp.BuildableClient)
	if !assert.True(t, ok, "expected the SDK's buildable HTTP client, got %T", cfg.HTTPClient) {
		return
	}
	transport := client.GetTransport()
	if !assert.NotNil(t, transport.TLSClientConfig) {
		return
	}
	assert.NotNil(t, transport.TLSClientConfig.RootCAs)
}

// TestSQLConnector_loadConfigWithCredentials_CABundleSurvivesAmbientProfile
// covers the intersection of the two fixes: a bogus AWS_PROFILE must not cost
// us the CA bundle. The environment-only path has to preserve the resolved
// environment settings rather than dropping to a bare config, and the
// caller's own region and credentials must still win over the default profile.
func TestSQLConnector_loadConfigWithCredentials_CABundleSurvivesAmbientProfile(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_CA_BUNDLE", writeTestCABundle(t))
	os.Setenv("AWS_PROFILE", "athenadriver-nonexistent-profile-regression")
	defer func() {
		os.Unsetenv("AWS_CA_BUNDLE")
		os.Unsetenv("AWS_PROFILE")
	}()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	cfg, err := connector.loadConfigWithCredentials(context.Background(),
		credentials.NewStaticCredentialsProvider("testid", "testkey", ""))

	assert.Nil(t, err)
	assert.Equal(t, "ap-southeast-1", cfg.Region)
	creds, err := cfg.Credentials.Retrieve(context.Background())
	assert.Nil(t, err)
	assert.Equal(t, "testid", creds.AccessKeyID)

	client, ok := cfg.HTTPClient.(*awshttp.BuildableClient)
	if !assert.True(t, ok, "expected the SDK's buildable HTTP client, got %T", cfg.HTTPClient) {
		return
	}
	transport := client.GetTransport()
	if !assert.NotNil(t, transport.TLSClientConfig) {
		return
	}
	assert.NotNil(t, transport.TLSClientConfig.RootCAs)
}

// TestSQLConnector_loadConfigWithCredentials_KeepsSDKDefaults is a
// regression test for a subtler version of the same loss: DefaultsMode,
// AccountIDEndpointMode, the compression settings, and the checksum settings
// are read by athena.NewFromConfig/s3.NewFromConfig off aws.Config directly,
// not out of ConfigSources. Leaving them zero is not "unconfigured" -- an
// Unset ResponseChecksumValidation disables S3 response checksum validation
// for query results, which the SDK otherwise performs by default.
func TestSQLConnector_loadConfigWithCredentials_KeepsSDKDefaults(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_PROFILE", "athenadriver-nonexistent-profile-regression")
	defer os.Unsetenv("AWS_PROFILE")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	cfg, err := connector.loadConfigWithCredentials(context.Background(),
		credentials.NewStaticCredentialsProvider("testid", "testkey", ""))

	assert.Nil(t, err)
	assert.Equal(t, aws.ResponseChecksumValidationWhenSupported, cfg.ResponseChecksumValidation)
	assert.Equal(t, aws.RequestChecksumCalculationWhenSupported, cfg.RequestChecksumCalculation)
	assert.Equal(t, aws.DefaultsModeLegacy, cfg.DefaultsMode)
	assert.Equal(t, aws.AccountIDEndpointMode(aws.AccountIDEndpointModePreferred), cfg.AccountIDEndpointMode)
	assert.Equal(t, int64(10240), cfg.RequestMinCompressSizeBytes)
	assert.False(t, cfg.DisableRequestCompression)
}

// TestSQLConnector_loadConfigWithCredentials_HonorsSDKEnv covers the other
// half: where those settings do have environment variables, they must be
// honored rather than replaced with the resolver defaults.
func TestSQLConnector_loadConfigWithCredentials_HonorsSDKEnv(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_PROFILE", "athenadriver-nonexistent-profile-regression")
	os.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
	os.Setenv("AWS_DEFAULTS_MODE", "standard")
	os.Setenv("AWS_DISABLE_REQUEST_COMPRESSION", "true")
	defer func() {
		os.Unsetenv("AWS_PROFILE")
		os.Unsetenv("AWS_RESPONSE_CHECKSUM_VALIDATION")
		os.Unsetenv("AWS_DEFAULTS_MODE")
		os.Unsetenv("AWS_DISABLE_REQUEST_COMPRESSION")
	}()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	cfg, err := connector.loadConfigWithCredentials(context.Background(),
		credentials.NewStaticCredentialsProvider("testid", "testkey", ""))

	assert.Nil(t, err)
	assert.Equal(t, aws.ResponseChecksumValidationWhenRequired, cfg.ResponseChecksumValidation)
	assert.Equal(t, aws.DefaultsModeStandard, cfg.DefaultsMode)
	assert.True(t, cfg.DisableRequestCompression)
}

// TestSQLConnector_loadConfigWithCredentials_PropagatesLoadError confirms
// that genuine environment errors still surface from the environment-only
// path: an unreadable AWS_CA_BUNDLE must fail the connection, not be skipped.
func TestSQLConnector_loadConfigWithCredentials_PropagatesLoadError(t *testing.T) {
	testConf := NewNoOpsConfig()
	_ = testConf.SetRegion("ap-southeast-1")
	os.Setenv("AWS_CA_BUNDLE", "/nonexistent/athenadriver-ca-bundle.pem")
	defer os.Unsetenv("AWS_CA_BUNDLE")
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}

	_, err := connector.loadConfigWithCredentials(context.Background(),
		credentials.NewStaticCredentialsProvider("testid", "testkey", ""))

	assert.NotNil(t, err)
}

func TestSQLConnector_Driver(t *testing.T) {
	testConf := NewNoOpsConfig()
	connector := &SQLConnector{
		config: testConf,
		tracer: NewDefaultObservability(testConf),
	}
	assert.NotNil(t, connector.Driver())
}
