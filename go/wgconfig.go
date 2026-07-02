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
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/athena/types"
)

// WGConfig wraps WorkGroupConfiguration.
type WGConfig struct {
	wgConfig *types.WorkGroupConfiguration
}

// wgConfigString renders a WorkGroupConfiguration in the same human-readable
// form the AWS SDK for Go v1 produced via (*WorkGroupConfiguration).String().
// The v2 types drop the generated String() method, so we reproduce it here to
// keep the driver's DSN representation stable across the SDK migration. Only the
// fields GetDefaultWGConfig / NewWGConfig ever populate are rendered, in the SDK
// field order, matching v1's awsutil.Prettify output.
func wgConfigString(c *types.WorkGroupConfiguration) string {
	if c == nil {
		return "{\n\n}"
	}
	var lines []string
	if c.BytesScannedCutoffPerQuery != nil {
		lines = append(lines, fmt.Sprintf("  BytesScannedCutoffPerQuery: %d", *c.BytesScannedCutoffPerQuery))
	}
	if c.EnforceWorkGroupConfiguration != nil {
		lines = append(lines, fmt.Sprintf("  EnforceWorkGroupConfiguration: %t", *c.EnforceWorkGroupConfiguration))
	}
	if c.PublishCloudWatchMetricsEnabled != nil {
		lines = append(lines, fmt.Sprintf("  PublishCloudWatchMetricsEnabled: %t", *c.PublishCloudWatchMetricsEnabled))
	}
	if c.RequesterPaysEnabled != nil {
		lines = append(lines, fmt.Sprintf("  RequesterPaysEnabled: %t", *c.RequesterPaysEnabled))
	}
	return "{\n" + strings.Join(lines, ",\n") + "\n}"
}

// GetDefaultWGConfig to create a default WorkGroupConfiguration.
func GetDefaultWGConfig() *types.WorkGroupConfiguration {
	var bytesScannedCutoffPerQuery int64 = DefaultBytesScannedCutoffPerQuery
	var enforceWorkGroupConfiguration bool = true
	var publishCloudWatchMetricsEnabled bool = true
	var requesterPaysEnabled bool = false
	return &types.WorkGroupConfiguration{
		BytesScannedCutoffPerQuery:      &bytesScannedCutoffPerQuery, // 1G by default
		EnforceWorkGroupConfiguration:   &enforceWorkGroupConfiguration,
		PublishCloudWatchMetricsEnabled: &publishCloudWatchMetricsEnabled,
		RequesterPaysEnabled:            &requesterPaysEnabled,
		ResultConfiguration:             nil,
	}
}

// NewWGConfig to create a WorkGroupConfiguration.
func NewWGConfig(bytesScannedCutoffPerQuery int64,
	enforceWorkGroupConfiguration bool,
	publishCloudWatchMetricsEnabled bool,
	requesterPaysEnabled bool,
	resultConfiguration *types.ResultConfiguration) *types.WorkGroupConfiguration {
	return &types.WorkGroupConfiguration{
		BytesScannedCutoffPerQuery:      &bytesScannedCutoffPerQuery,
		EnforceWorkGroupConfiguration:   &enforceWorkGroupConfiguration,
		PublishCloudWatchMetricsEnabled: &publishCloudWatchMetricsEnabled,
		RequesterPaysEnabled:            &requesterPaysEnabled,
		ResultConfiguration:             resultConfiguration,
	}
}
