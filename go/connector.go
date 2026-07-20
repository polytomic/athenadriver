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
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"fmt"
	"net/http"

	"os"
	"strconv"
	"time"

	"github.com/uber-go/tally/v4"
	"go.uber.org/zap"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go/logging"
)

// SQLConnector is the connector for AWS Athena Driver.
type SQLConnector struct {
	config *Config
	tracer *DriverTracer
}

// NoopsSQLConnector is to create a noops SQLConnector.
func NoopsSQLConnector() *SQLConnector {
	noopsConfig := NewNoOpsConfig()
	return &SQLConnector{
		config: noopsConfig,
		tracer: NewDefaultObservability(noopsConfig),
	}
}

// Driver is to construct a new SQLConnector.
func (c *SQLConnector) Driver() driver.Driver {
	return &SQLDriver{}
}

// Connect is to create an AWS config and Athena/S3 clients.
// The order to find auth information is:
// 1. Manually set AWS profile in Config by calling config.SetAWSProfile(profileName)
// 2. AWS_SDK_LOAD_CONFIG
// 3. IAM Role Assumption with optional External ID
// 4. Static Credentials
// Ref: https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html
func (c *SQLConnector) Connect(ctx context.Context) (driver.Conn, error) {
	now := time.Now()
	c.tracer = NewDefaultObservability(c.config)
	if metrics, ok := ctx.Value(MetricsKey).(tally.Scope); ok {
		c.tracer.SetScope(metrics)
	}
	if logger, ok := ctx.Value(LoggerKey).(*zap.Logger); ok {
		c.tracer.SetLogger(logger)
	}

	var cfg aws.Config
	var err error
	// respect AWS_SDK_LOAD_CONFIG and local ~/.aws/credentials, ~/.aws/config
	if ok, _ := strconv.ParseBool(os.Getenv("AWS_SDK_LOAD_CONFIG")); ok {
		// The v2 SDK always loads shared config, so this branch only needs to
		// honor an explicitly-selected profile.
		if profile := c.config.GetAWSProfile(); profile != "" {
			cfg, err = config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile))
		} else {
			cfg, err = config.LoadDefaultConfig(ctx)
		}
	} else if roleArn := c.config.GetRoleArn(); roleArn != "" {
		// IAM Role Assumption with optional External ID
		// First, create a base config for assuming the role.
		var baseCfg aws.Config
		baseCfg, err = c.createBaseConfig(ctx)
		if err != nil {
			c.tracer.Scope().Counter(DriverName + ".failure.sqlconnector.newsession").Inc(1)
			return nil, err
		}

		stsClient := sts.NewFromConfig(baseCfg)
		provider := stscreds.NewAssumeRoleProvider(stsClient, roleArn, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = c.config.GetRoleSessionName()
			if externalID := c.config.GetExternalID(); externalID != "" {
				o.ExternalID = aws.String(externalID)
			}
			// Add session tags if configured
			if sessionTags := c.config.GetSessionTags(); len(sessionTags) > 0 {
				for key, value := range sessionTags {
					o.Tags = append(o.Tags, ststypes.Tag{
						Key:   aws.String(key),
						Value: aws.String(value),
					})
				}
			}
		})

		// v1's stscreds.NewCredentials cached implicitly; v2's provider does
		// not, so wrap it in a credentials cache.
		cfg, err = c.loadConfigWithCredentials(ctx, aws.NewCredentialsCache(provider))
	} else if c.config.GetAccessID() != "" {
		cfg, err = c.loadConfigWithCredentials(ctx, credentials.NewStaticCredentialsProvider(
			c.config.GetAccessID(),
			c.config.GetSecretAccessKey(),
			c.config.GetSessionToken(),
		))
	} else {
		// Default credential chain (environment variables, EC2 instance
		// profile, IRSA, etc.). LoadDefaultConfig defers credential resolution
		// to first use, so it does not error when no keys are present.
		cfg, err = c.loadDefaultChainConfig(ctx)
	}
	if err != nil {
		c.tracer.Scope().Counter(DriverName + ".failure.sqlconnector.newsession").Inc(1)
		return nil, err
	}

	athenaAPI := athena.NewFromConfig(cfg)
	s3API := s3.NewFromConfig(cfg)
	downloaderAPI := manager.NewDownloader(s3API)
	timeConnect := time.Since(now)
	conn := &Connection{
		athenaAPI: athenaAPI,
		s3:        s3API,
		s3mgr:     downloaderAPI,
		connector: c,
	}
	c.tracer.Scope().Timer(DriverName + ".connector.connect").Record(timeConnect)
	return conn, nil
}

// loadConfigWithCredentials builds an aws.Config for a connection that
// supplies its own credentials -- static keys or an assume-role provider.
//
// It deliberately does not go through LoadDefaultConfig. The v2 SDK always
// parses the ambient shared config, regardless of AWS_SDK_LOAD_CONFIG, and
// offers no option to skip it; with AWS_PROFILE set, resolveConfigLoaders in
// the config package even parses it strictly, so an absent, malformed, or
// merely incomplete profile would fail a connection that needs no profile at
// all. And when parsing succeeds, profile settings (use_fips_endpoint,
// endpoint URLs, retry modes, ...) would silently apply to a connection whose
// DSN specified its credentials explicitly.
//
// v1's session.NewSession read only the environment on these paths, and that
// is the contract kept here: explicit credentials plus environment settings
// (AWS_CA_BUNDLE, the FIPS/dual-stack toggles, retry variables), never the
// shared profile.
func (c *SQLConnector) loadConfigWithCredentials(_ context.Context, creds aws.CredentialsProvider) (aws.Config, error) {
	return c.configFromEnv(creds)
}

// configFromEnv builds an aws.Config from environment configuration alone,
// with no shared-profile parsing. It reproduces the SDK's resolvers for everything a client can
// observe from the environment: region, credentials, AWS_CA_BUNDLE, the
// endpoint settings that service clients read back out of ConfigSources
// (FIPS, dual-stack, AWS_ENDPOINT_URL), and the settings clients read off
// aws.Config directly -- including their defaults, since an unset field there
// means "off", not "unconfigured". Shared-profile-only settings are, by
// construction, not carried over -- that is the point.
func (c *SQLConnector) configFromEnv(creds aws.CredentialsProvider) (aws.Config, error) {
	envCfg, err := config.NewEnvConfig()
	if err != nil {
		return aws.Config{}, err
	}

	cfg := aws.Config{
		Region:           c.config.GetRegion(),
		Credentials:      creds,
		Logger:           logging.NewStandardLogger(os.Stderr),
		ConfigSources:    []interface{}{envCfg},
		AppID:            envCfg.AppID,
		RetryMaxAttempts: envCfg.RetryMaxAttempts,
		RetryMode:        envCfg.RetryMode,

		// Settings below are read by service clients off aws.Config itself
		// rather than out of ConfigSources, so their environment values and
		// resolver defaults have to be reproduced here. Leaving them zero is
		// not equivalent to "unconfigured": an Unset ResponseChecksumValidation
		// silently turns off S3 response checksum validation, which the SDK
		// enables by default.
		DefaultsMode:               defaultsModeOrDefault(envCfg.DefaultsMode),
		AccountIDEndpointMode:      accountIDEndpointModeOrDefault(envCfg.AccountIDEndpointMode),
		RequestChecksumCalculation: requestChecksumOrDefault(envCfg.RequestChecksumCalculation),
		ResponseChecksumValidation: responseChecksumOrDefault(envCfg.ResponseChecksumValidation),
		RequestMinCompressSizeBytes: derefOr(
			envCfg.RequestMinCompressSizeBytes, defaultRequestMinCompressSizeBytes),
		DisableRequestCompression: derefOr(envCfg.DisableRequestCompression, false),
		AuthSchemePreference:      envCfg.AuthSchemePreference,
	}
	if cfg.DefaultsMode == aws.DefaultsModeAuto {
		// The config package additionally asks IMDS for the instance region
		// here; that call is skipped rather than made on the connection path,
		// so auto mode resolves without the in-region/cross-region signal.
		cfg.RuntimeEnvironment = aws.RuntimeEnvironment{
			EnvironmentIdentifier: aws.ExecutionEnvironmentID(os.Getenv("AWS_EXECUTION_ENV")),
			Region:                envCfg.Region,
		}
	}
	if envCfg.BaseEndpoint != "" {
		cfg.BaseEndpoint = aws.String(envCfg.BaseEndpoint)
	}
	if envCfg.CustomCABundle != "" {
		client, err := caBundleClient(envCfg.CustomCABundle)
		if err != nil {
			return aws.Config{}, err
		}
		cfg.HTTPClient = client
	}
	return cfg, nil
}

// defaultRequestMinCompressSizeBytes mirrors the fallback the config package's
// resolveRequestMinCompressSizeBytes applies when nothing configures it.
const defaultRequestMinCompressSizeBytes = 10240

func defaultsModeOrDefault(m aws.DefaultsMode) aws.DefaultsMode {
	if m == "" {
		return aws.DefaultsModeLegacy
	}
	return m
}

func accountIDEndpointModeOrDefault(m aws.AccountIDEndpointMode) aws.AccountIDEndpointMode {
	if m == "" {
		return aws.AccountIDEndpointModePreferred
	}
	return m
}

func requestChecksumOrDefault(c aws.RequestChecksumCalculation) aws.RequestChecksumCalculation {
	if c == 0 {
		return aws.RequestChecksumCalculationWhenSupported
	}
	return c
}

func responseChecksumOrDefault(v aws.ResponseChecksumValidation) aws.ResponseChecksumValidation {
	if v == 0 {
		return aws.ResponseChecksumValidationWhenSupported
	}
	return v
}

func derefOr[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}

// caBundleClient mirrors the config package's resolveCustomCABundle for the
// environment-only path above, so that AWS_CA_BUNDLE keeps applying -- and
// keeps failing loudly when it names a file that is missing or not PEM.
func caBundleClient(bundlePath string) (aws.HTTPClient, error) {
	pem, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read custom CA bundle PEM file: %w", err)
	}

	var appendErr error
	client := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		if tr.TLSClientConfig.RootCAs == nil {
			tr.TLSClientConfig.RootCAs = x509.NewCertPool()
		}
		if !tr.TLSClientConfig.RootCAs.AppendCertsFromPEM(pem) {
			appendErr = fmt.Errorf("failed to load custom CA bundle PEM file")
		}
	})
	if appendErr != nil {
		return nil, appendErr
	}
	return client, nil
}

// createBaseConfig creates a base AWS config for assuming a role.
// This config uses credentials from either static config, environment
// variables, or the default credential chain.
func (c *SQLConnector) createBaseConfig(ctx context.Context) (aws.Config, error) {
	if c.config.GetAccessID() != "" {
		// Use static credentials if provided.
		return c.loadConfigWithCredentials(ctx, credentials.NewStaticCredentialsProvider(
			c.config.GetAccessID(),
			c.config.GetSecretAccessKey(),
			c.config.GetSessionToken(),
		))
	}

	// Fall back to default credential chain (environment variables, EC2 instance profile, etc.)
	return c.loadDefaultChainConfig(ctx)
}

// loadDefaultChainConfig builds an aws.Config whose credentials come from the
// SDK's default provider chain -- environment variables, web-identity/IRSA, and
// ECS/EC2 container or instance roles.
//
// Under v1, session.NewSession with AWS_SDK_LOAD_CONFIG unset did not read
// ~/.aws/config on this path, and an absent profile fell through to the next
// provider in the chain. The v2 SDK always parses the shared config and
// credentials files inside LoadDefaultConfig, and -- when AWS_PROFILE is set --
// parses them strictly (see resolveConfigLoaders in the config package). A stray
// or malformed ~/.aws/config, or a profile that cannot be resolved, therefore
// fails the whole load before the EC2/container/IRSA providers are ever tried.
// That regressed connections that rely on the default chain, including the
// assume-role base config below, whose identity typically comes from IRSA or an
// instance role rather than a profile.
//
// Simply dropping the shared files is not an option: an AWS_PROFILE that names a
// profile defined only in ~/.aws/config (the common local/CLI setup) would then
// fail the strict load, breaking a configuration that works today. So the load
// is attempted normally first -- honoring a genuinely-present profile and any
// settings it carries -- and only if that fails is it retried with the malformed
// ~/.aws/config removed, so ambient shared configuration cannot block a
// connection whose credentials are meant to come from the environment or an
// instance/container role. The ~/.aws/credentials file is deliberately kept on
// the retry: under v1 (AWS_SDK_LOAD_CONFIG unset) that file was still read even
// though ~/.aws/config was ignored, so a valid default or AWS_PROFILE entry
// living only in ~/.aws/credentials must keep authenticating. The SDK's own
// chain resolution (and its container-endpoint host checks) is used in both
// attempts. Explicit credentials take a different path (configFromEnv) that
// ignores the shared files entirely, since there the DSN already supplied the
// credentials.
func (c *SQLConnector) loadDefaultChainConfig(ctx context.Context) (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.config.GetRegion()))
	if err == nil {
		return cfg, nil
	}

	// The initial load failed. If credentials are available from the
	// environment, an instance/container role, or ~/.aws/credentials, the
	// failure came from parsing ~/.aws/config, not from the chain itself --
	// retry with only that file suppressed. ~/.aws/credentials is left in place
	// so a valid default or AWS_PROFILE entry there still resolves, matching the
	// v1 behavior. The original error is returned if this fallback also fails,
	// since it saw the real files and is the more informative of the two.
	//
	// A profile the caller explicitly selected via AWS_PROFILE that resolves in
	// neither the environment nor ~/.aws/credentials is not recoverable here:
	// the strict loader still fails on the retry, and that is intentional -- an
	// unresolvable explicit selection is a real misconfiguration, not the
	// ambient noise this guards against.
	cfg, fallbackErr := config.LoadDefaultConfig(ctx,
		config.WithRegion(c.config.GetRegion()),
		config.WithSharedConfigFiles([]string{}),
	)
	if fallbackErr != nil {
		return aws.Config{}, err
	}
	return cfg, nil
}
