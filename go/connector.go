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
	"errors"
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
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(c.config.GetRegion()))
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

// loadConfigWithCredentials builds an aws.Config that uses the supplied
// explicit credentials while retaining the SDK's resolved environment
// settings -- notably AWS_CA_BUNDLE and the FIPS/dualstack endpoint toggles,
// both of which v1's session.NewSession applied regardless of
// AWS_SDK_LOAD_CONFIG.
//
// LoadDefaultConfig parses the ambient shared profile before it honors
// WithCredentialsProvider, and it parses it strictly whenever AWS_PROFILE is
// set -- see resolveConfigLoaders in the config package, which only tolerates
// a missing profile when AWS_PROFILE is empty. A profile that is absent,
// malformed, or merely incomplete (credential_source without role_arn, say)
// therefore fails a connection that supplied its own credentials and needs no
// profile at all. No LoadOptions setting relaxes this: the shared config
// loader runs before any option is consulted, so pinning the profile or
// emptying the file list does not help, and neither can the fallback go
// through LoadDefaultConfig.
//
// So when a load failure is attributable to the shared configuration, rebuild
// from the environment alone, which does no profile parsing. Genuine
// environment errors -- an unreadable or malformed AWS_CA_BUNDLE, an invalid
// AWS_USE_FIPS_ENDPOINT -- still surface, from either attempt.
func (c *SQLConnector) loadConfigWithCredentials(ctx context.Context, creds aws.CredentialsProvider) (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(c.config.GetRegion()),
		config.WithCredentialsProvider(creds),
	)
	if err == nil {
		return cfg, nil
	}
	if !sharedConfigIsAtFault(ctx) {
		return aws.Config{}, err
	}
	return c.configFromEnv(creds)
}

// sharedConfigIsAtFault reports whether the ambient shared configuration is
// itself unloadable, and so is the likely cause of a LoadDefaultConfig
// failure. Attributing by behavior rather than by error type is deliberate:
// several shared-config validation failures are bare fmt.Errorf values with
// no type to match on.
//
// A profile that simply does not exist counts only when AWS_PROFILE named it.
// With AWS_PROFILE unset the SDK already tolerates an absent default profile,
// so a load failure in that case came from somewhere else and must not be
// swallowed.
func sharedConfigIsAtFault(ctx context.Context) bool {
	envCfg, err := config.NewEnvConfig()
	if err != nil {
		return false
	}

	profile := envCfg.SharedConfigProfile
	named := profile != ""
	if !named {
		profile = config.DefaultSharedConfigProfile
	}

	// LoadSharedConfigProfile defaults to ~/.aws/{config,credentials} and does
	// not consult AWS_CONFIG_FILE or AWS_SHARED_CREDENTIALS_FILE itself, so
	// point it at the same files LoadDefaultConfig just used.
	_, err = config.LoadSharedConfigProfile(ctx, profile, func(o *config.LoadSharedConfigOptions) {
		if envCfg.SharedConfigFile != "" {
			o.ConfigFiles = []string{envCfg.SharedConfigFile}
		}
		if envCfg.SharedCredentialsFile != "" {
			o.CredentialsFiles = []string{envCfg.SharedCredentialsFile}
		}
	})
	if err == nil {
		return false
	}

	var notExist config.SharedConfigProfileNotExistError
	if errors.As(err, &notExist) && !named {
		return false
	}
	return true
}

// configFromEnv builds an aws.Config from environment configuration alone,
// skipping the shared-profile parsing that LoadDefaultConfig cannot be told
// to skip. It covers the subset of the SDK's resolvers that a connection with
// explicit credentials depends on: region, credentials, AWS_CA_BUNDLE, and
// the endpoint settings that service clients read back out of ConfigSources
// (FIPS, dual-stack, AWS_ENDPOINT_URL). Shared-profile-only settings are, by
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
	return config.LoadDefaultConfig(ctx, config.WithRegion(c.config.GetRegion()))
}
