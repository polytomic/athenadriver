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
	"io"

	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// athenaClient is the subset of the AWS SDK for Go v2 Athena client used by the
// driver. It replaces the v1 athenaiface.AthenaAPI so results can be mocked in
// tests without pulling in an interface package (which v2 does not provide).
type athenaClient interface {
	StartQueryExecution(context.Context, *athena.StartQueryExecutionInput, ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error)
	GetQueryExecution(context.Context, *athena.GetQueryExecutionInput, ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error)
	StopQueryExecution(context.Context, *athena.StopQueryExecutionInput, ...func(*athena.Options)) (*athena.StopQueryExecutionOutput, error)
	GetQueryResults(context.Context, *athena.GetQueryResultsInput, ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error)
	GetQueryRuntimeStatistics(context.Context, *athena.GetQueryRuntimeStatisticsInput, ...func(*athena.Options)) (*athena.GetQueryRuntimeStatisticsOutput, error)
	GetWorkGroup(context.Context, *athena.GetWorkGroupInput, ...func(*athena.Options)) (*athena.GetWorkGroupOutput, error)
	CreateWorkGroup(context.Context, *athena.CreateWorkGroupInput, ...func(*athena.Options)) (*athena.CreateWorkGroupOutput, error)
}

// s3Client is the subset of the AWS SDK for Go v2 S3 client used by the driver.
// It replaces the v1 s3iface.S3API.
type s3Client interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// s3DownloaderAPI is the subset of the AWS SDK for Go v2 s3manager Downloader
// used by the driver. It replaces the v1 s3manageriface.DownloaderAPI.
type s3DownloaderAPI interface {
	Download(ctx context.Context, w io.WriterAt, input *s3.GetObjectInput, options ...func(*manager.Downloader)) (int64, error)
}
