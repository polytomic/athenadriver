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
	"github.com/aws/aws-sdk-go-v2/config"
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

// The default credential chain -- used both for a bare connection and as the
// base config for an assume-role connection without static credentials -- must
// not be blocked by a stray or malformed ~/.aws/config. v1's session did not
// read that file on this path (AWS_SDK_LOAD_CONFIG unset); v2 does, and fails
// the whole load when it cannot be parsed, before the EC2/container/IRSA
// providers are ever tried. loadDefaultChainConfig leaves ~/.aws/config unread
// to restore the v1 contract while keeping the SDK's chain resolution intact.
func TestLoadDefaultChainConfig_IgnoresBrokenConfigFile(t *testing.T) {
	// v1 never read ~/.aws/config on this path, so a broken [default] profile
	// there was ignored. Under v2 that same profile is resolved eagerly by
	// LoadDefaultConfig and fails the load. Each of these fails a raw load (see
	// the precondition below) but must be tolerated once the config file is
	// dropped.
	configFiles := []struct {
		name     string
		contents string
	}{
		{"two credential types", "[default]\ncredential_source = Ec2InstanceMetadata\ncredential_process = /bin/true\n"},
		{"source_profile pointing at nothing", "[default]\nrole_arn = arn:aws:iam::1:role/r\nsource_profile = ghost\n"},
		{"credential_source without role_arn", "[default]\ncredential_source = Ec2InstanceMetadata\n"},
	}

	for _, cf := range configFiles {
		t.Run(cf.name, func(t *testing.T) {
			ambientAWSEnv(t, cf.contents)
			// No AWS_PROFILE selected: this is the default-chain case, where the
			// runtime identity comes from IRSA or an instance/container role.
			t.Setenv("AWS_PROFILE", "")

			// Precondition: the raw LoadDefaultConfig this path used to call
			// really does fail on this ~/.aws/config, so the assertions below
			// are exercising the fix rather than a no-op.
			_, rawErr := config.LoadDefaultConfig(context.Background(),
				config.WithRegion("us-east-1"))
			require.Error(t, rawErr,
				"precondition: raw default-chain load should fail on the broken config file")

			c := testConnector(t)

			cfg, err := c.loadDefaultChainConfig(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "us-east-1", cfg.Region)
			assert.NotNil(t, cfg.Credentials,
				"default chain should still be wired up for lazy resolution")

			// The assume-role base config (no static credentials) takes the same
			// fallback and must be just as tolerant.
			baseCfg, err := c.createBaseConfig(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "us-east-1", baseCfg.Region)
		})
	}
}

// v1's SharedConfigDisable still read ~/.aws/credentials, so a genuinely-present
// AWS_PROFILE must keep resolving from that file. Only ~/.aws/config is dropped.
func TestLoadDefaultChainConfig_HonorsCredentialsFileProfile(t *testing.T) {
	dir := ambientAWSEnv(t,
		// A broken ~/.aws/config that would fail a strict load if it were read.
		"[profile broken]\ncredential_source = Nonsense\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "credentials"),
		[]byte("[work]\naws_access_key_id = AKIAPROFILE\naws_secret_access_key = profilesecret\n"), 0600))
	t.Setenv("AWS_PROFILE", "work")

	c := testConnector(t)

	cfg, err := c.loadDefaultChainConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", cfg.Region)

	// The credentials come from ~/.aws/credentials, not the broken config file.
	creds, err := cfg.Credentials.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKIAPROFILE", creds.AccessKeyID)
}

// A profile the caller explicitly selects via AWS_PROFILE but that exists in
// neither shared file is a real misconfiguration, not ambient noise: the strict
// v2 loader fails on it and the fallback cannot (and should not) paper over it.
func TestLoadDefaultChainConfig_ExplicitMissingProfileStillErrors(t *testing.T) {
	ambientAWSEnv(t, "[profile other]\nregion = us-west-2\n")
	t.Setenv("AWS_PROFILE", "absent")

	c := testConnector(t)
	_, err := c.loadDefaultChainConfig(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent")
}
