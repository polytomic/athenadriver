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
	"database/sql/driver"
	"errors"

	"os"
	"strconv"
	"time"

	"github.com/uber-go/tally/v4"
	"go.uber.org/zap"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
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
// settings -- notably AWS_CA_BUNDLE, the client TLS cert/key pair, and the
// FIPS/dualstack endpoint toggles, all of which v1's session.NewSession
// applied regardless of AWS_SDK_LOAD_CONFIG.
//
// LoadDefaultConfig parses shared config before honoring
// WithCredentialsProvider, so an unrelated AWS_PROFILE pointing at an absent
// profile would otherwise fail an entirely self-contained connection. That
// particular failure is not meaningful here -- the caller supplied the
// credentials, so no profile is needed -- so retry pinned to the default
// profile, which the SDK tolerates being absent. Explicitly-supplied region
// and credentials still take precedence over anything a default profile
// defines, and the retry keeps the resolved environment settings that a bare
// aws.Config literal would discard. Any other load error is real and is
// returned.
func (c *SQLConnector) loadConfigWithCredentials(ctx context.Context, creds aws.CredentialsProvider) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(c.config.GetRegion()),
		config.WithCredentialsProvider(creds),
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err == nil {
		return cfg, nil
	}

	var profileErr config.SharedConfigProfileNotExistError
	if errors.As(err, &profileErr) {
		return config.LoadDefaultConfig(ctx, append(opts,
			config.WithSharedConfigProfile("default"))...)
	}
	return aws.Config{}, err
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
