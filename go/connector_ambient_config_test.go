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
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ambientAWSEnv isolates the process from the developer's real ~/.aws and
// points the SDK at a scratch config file with the given contents.
func ambientAWSEnv(t *testing.T, configContents string) string {
	t.Helper()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte(configContents), 0600))

	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CA_BUNDLE", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")
	t.Setenv("AWS_USE_FIPS_ENDPOINT", "")

	return dir
}

func testConnector(t *testing.T) *SQLConnector {
	t.Helper()

	conf, err := NewDefaultConfig("s3://bucket/", "us-east-1", "AKIAFAKE", "secret")
	require.NoError(t, err)
	return &SQLConnector{config: conf, tracer: NewDefaultObservability(conf)}
}

func staticCreds() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("AKIAFAKE", "secret", "")
}

// Explicit credentials must be unaffected by ambient shared-profile
// configuration, however broken: the explicit-credential path never reads it.
func TestLoadConfigWithCredentials_IgnoresAmbientProfileFailures(t *testing.T) {
	tests := []struct {
		name       string
		configFile string
		profileEnv string
	}{
		{
			name:       "profile named by AWS_PROFILE does not exist",
			configFile: "[profile other]\nregion = us-west-2\n",
			profileEnv: "absent",
		},
		{
			// Not a SharedConfigProfileNotExistError: regression coverage from
			// when a fallback heuristic type-matched on the load error.
			name:       "credential_source without role_arn",
			configFile: "[profile broken]\ncredential_source = Ec2InstanceMetadata\n",
			profileEnv: "broken",
		},
		{
			name:       "source_profile pointing at nothing",
			configFile: "[profile broken]\nrole_arn = arn:aws:iam::1:role/r\nsource_profile = ghost\n",
			profileEnv: "broken",
		},
		{
			name:       "two credential types in one profile",
			configFile: "[profile broken]\ncredential_process = /bin/true\ncredential_source = Ec2InstanceMetadata\n",
			profileEnv: "broken",
		},
		{
			// No AWS_PROFILE, but the default profile the SDK falls back to is
			// itself malformed.
			name:       "malformed default profile",
			configFile: "[default]\ncredential_source = Ec2InstanceMetadata\n",
			profileEnv: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ambientAWSEnv(t, tt.configFile)
			t.Setenv("AWS_PROFILE", tt.profileEnv)

			c := testConnector(t)
			cfg, err := c.loadConfigWithCredentials(context.Background(), staticCreds())
			require.NoError(t, err)

			assert.Equal(t, "us-east-1", cfg.Region)
			creds, err := cfg.Credentials.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "AKIAFAKE", creds.AccessKeyID)
		})
	}
}

// A healthy ambient profile is ignored just as thoroughly as a broken one.
// Explicit-credential connections read only the environment, matching the v1
// SDK's behavior with AWS_SDK_LOAD_CONFIG unset, so profile settings must not
// leak into the client config.
func TestLoadConfigWithCredentials_IgnoresHealthyProfileSettings(t *testing.T) {
	ambientAWSEnv(t,
		"[profile fine]\nregion = us-west-2\nuse_fips_endpoint = true\nmax_attempts = 99\n")
	t.Setenv("AWS_PROFILE", "fine")

	c := testConnector(t)
	cfg, err := c.loadConfigWithCredentials(context.Background(), staticCreds())
	require.NoError(t, err)

	assert.Equal(t, "us-east-1", cfg.Region)
	creds, err := cfg.Credentials.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKIAFAKE", creds.AccessKeyID)

	// max_attempts from the profile must not apply.
	assert.Zero(t, cfg.RetryMaxAttempts)

	// Nor use_fips_endpoint: the env config in ConfigSources reports it unset.
	require.Len(t, cfg.ConfigSources, 1)
	src, ok := cfg.ConfigSources[0].(interface {
		GetUseFIPSEndpoint(context.Context) (aws.FIPSEndpointState, bool, error)
	})
	require.True(t, ok, "ConfigSources should expose the env config")
	_, found, err := src.GetUseFIPSEndpoint(context.Background())
	require.NoError(t, err)
	assert.False(t, found, "profile use_fips_endpoint should not be visible")
}

// Environment settings still apply on the explicit-credential path.
func TestLoadConfigWithCredentials_KeepsEnvSettings(t *testing.T) {
	ambientAWSEnv(t, "[profile broken]\ncredential_source = Ec2InstanceMetadata\n")
	t.Setenv("AWS_PROFILE", "broken")

	t.Setenv("AWS_CA_BUNDLE", writeTestCABundle(t))
	t.Setenv("AWS_MAX_ATTEMPTS", "7")
	t.Setenv("AWS_USE_FIPS_ENDPOINT", "true")

	c := testConnector(t)
	cfg, err := c.loadConfigWithCredentials(context.Background(), staticCreds())
	require.NoError(t, err)

	assert.NotNil(t, cfg.HTTPClient, "AWS_CA_BUNDLE should have produced a custom HTTP client")
	assert.Equal(t, 7, cfg.RetryMaxAttempts)

	// FIPS/dual-stack are not fields on aws.Config; service clients read them
	// back out of ConfigSources, so that wiring has to be preserved.
	require.Len(t, cfg.ConfigSources, 1)
	src, ok := cfg.ConfigSources[0].(interface {
		GetUseFIPSEndpoint(context.Context) (aws.FIPSEndpointState, bool, error)
	})
	require.True(t, ok, "ConfigSources should expose the env config")
	state, found, err := src.GetUseFIPSEndpoint(context.Background())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, aws.FIPSEndpointStateEnabled, state)
}

// A genuinely broken transport setting is a real error and must surface,
// whether or not the ambient profile is also bad.
func TestLoadConfigWithCredentials_InvalidCABundleStillErrors(t *testing.T) {
	tests := []struct {
		name       string
		configFile string
		profileEnv string
	}{
		{"healthy profile", "[profile fine]\nregion = us-west-2\n", "fine"},
		{"broken profile too", "[profile broken]\ncredential_source = Ec2InstanceMetadata\n", "broken"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/not PEM", func(t *testing.T) {
			dir := ambientAWSEnv(t, tt.configFile)
			t.Setenv("AWS_PROFILE", tt.profileEnv)

			bundle := filepath.Join(dir, "ca.pem")
			require.NoError(t, os.WriteFile(bundle, []byte("not a certificate"), 0600))
			t.Setenv("AWS_CA_BUNDLE", bundle)

			c := testConnector(t)
			_, err := c.loadConfigWithCredentials(context.Background(), staticCreds())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "custom CA bundle")
		})

		t.Run(tt.name+"/missing file", func(t *testing.T) {
			dir := ambientAWSEnv(t, tt.configFile)
			t.Setenv("AWS_PROFILE", tt.profileEnv)
			t.Setenv("AWS_CA_BUNDLE", filepath.Join(dir, "nope.pem"))

			c := testConnector(t)
			_, err := c.loadConfigWithCredentials(context.Background(), staticCreds())
			require.Error(t, err)
		})
	}
}
