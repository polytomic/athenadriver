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
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/athena/types"
)

// WGConfig wraps WorkGroupConfiguration.
type WGConfig struct {
	wgConfig *types.WorkGroupConfiguration
}

// wgConfigString renders a WorkGroupConfiguration in the same human-readable
// form the AWS SDK for Go v1 produced via (*WorkGroupConfiguration).String(),
// which delegated to awsutil.Prettify. The v2 types drop the generated String()
// method, so we reimplement Prettify's reflection walk here to keep the driver's
// DSN representation stable across the SDK migration. Every populated field is
// rendered — not a fixed whitelist — so configurations that set fields such as
// AdditionalConfiguration, EngineVersion, or ExecutionRole retain their
// serialized representation, matching v1: fields walked in struct-declaration
// order, unset fields omitted, strings quoted, nested structs indented two more
// spaces.
func wgConfigString(c *types.WorkGroupConfiguration) string {
	if c == nil {
		return "{\n\n}"
	}
	var buf strings.Builder
	prettifyWGConfig(reflect.ValueOf(c), 0, &buf)
	return buf.String()
}

// prettifyWGConfig reproduces the subset of
// github.com/aws/aws-sdk-go/aws/awsutil.Prettify that the workgroup
// configuration types exercise: structs, pointers, and scalar fields (no slices
// or maps appear in WorkGroupConfiguration, so those fall through to %v).
//
// Two adjustments preserve v1 parity against v2's regenerated types: v1 modeled
// string enums (EncryptionOption, S3AclOption, AuthenticationType, ...) as
// *string, whereas v2 models them as named string values. So an empty enum is
// treated as unset and omitted — matching a nil *string in v1 — and non-empty
// enums are quoted exactly as v1's *string fields were.
func prettifyWGConfig(v reflect.Value, indent int, buf *strings.Builder) {
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		buf.WriteString("{\n")
		pad := strings.Repeat(" ", indent+2)
		first := true
		for i := 0; i < v.Type().NumField(); i++ {
			ft := v.Type().Field(i)
			f := v.Field(i)
			if !ft.IsExported() {
				continue
			}
			if (f.Kind() == reflect.Ptr || f.Kind() == reflect.Slice || f.Kind() == reflect.Map) && f.IsNil() {
				continue
			}
			// Empty named-string enums are the zero value and, like a nil
			// *string in v1, are treated as unset.
			if f.Kind() == reflect.String && f.Len() == 0 {
				continue
			}
			if !first {
				buf.WriteString(",\n")
			}
			first = false
			buf.WriteString(pad)
			buf.WriteString(ft.Name)
			buf.WriteString(": ")
			prettifyWGConfig(f, indent+2, buf)
		}
		buf.WriteString("\n")
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString("}")
	case reflect.String:
		fmt.Fprintf(buf, "%q", v.String())
	default:
		fmt.Fprintf(buf, "%v", v.Interface())
	}
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
