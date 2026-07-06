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
	"fmt"
	"io"
	"net/http"
	"strings"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/athena/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// genQueryResultsOutputByToken is a function type with string as parameter.
type genQueryResultsOutputByToken func(token string) (*athena.GetQueryResultsOutput, error)

// newRowsSuccessIDs are the query IDs for which GetQueryExecution should return
// a successful QueryExecution (with a result-set S3 location) so NewRows can be
// constructed directly in tests. Each maps to a CSV fixture served by
// mockS3Client for the actual row data (see csvFixtures).
var newRowsSuccessIDs = map[string]bool{
	"SELECT_OK":                               true,
	"SELECT_GetQueryResults_ERR":              true,
	"SELECT_EMPTY_ROW_IN_PAGE":                true,
	"show":                                    true,
	"RowsNextFailed":                          true,
	"1coloumn0row":                            true,
	"1coloumn0row_valid":                      true,
	"column_more_than_row_fields":             true,
	"row_fields_more_than_column":             true,
	"missing_data_resp":                       true,
	"missing_data_resp2":                      true,
	"GetQueryResultsWithContext_return_error": true,
	"00000000-0000-0000-0000-000000000000":    true,
}

// mockAthenaClient implements the athenaClient interface for testing. Unlike the
// AWS SDK for Go v1 (which offered an embeddable athenaiface.AthenaAPI), v2 has
// no interface package, so the mock implements the seven methods directly.
type mockAthenaClient struct {
	// queryToResultsGenMap maps a query ID to a function generating its
	// GetQueryResults column metadata (the row values are served as CSV from
	// mockS3Client).
	queryToResultsGenMap map[string]genQueryResultsOutputByToken

	CreateWGStatus bool
	GetWGStatus    bool
	WGDisabled     bool
}

func newMockAthenaClient() *mockAthenaClient {
	var m = mockAthenaClient{
		queryToResultsGenMap: map[string]genQueryResultsOutputByToken{
			"SELECT_OK":                            MultiplePagesQueryResponse,
			"SELECT_GetQueryResults_ERR":           MultiplePagesQueryResponse,
			"SELECT_EMPTY_ROW_IN_PAGE":             MultiplePagesQueryResponse,
			"show":                                 ShowResponse,
			"RowsNextFailed":                       NextFailedResponse,
			"1coloumn0row":                         OneColumnZeroRowResponse,
			"1coloumn0row_valid":                   OneColumnZeroRowResponseValid,
			"column_more_than_row_fields":          ColumnMoreThanRowFieldResponse,
			"row_fields_more_than_column":          RowFieldMoreThanColumnsResponse,
			"missing_data_resp":                    MissingDataResponse,
			"missing_data_resp2":                   headPageWithColumnButNoRowResponse,
			"PING_OK_QID":                          PingResponse,
			"SELECTExecContext_OK_QID":             PingResponse,
			"SELECTQueryContext_OK_QID":            PingResponse,
			"00000000-0000-0000-0000-000000000000": PingResponse,
			"pc:get_query_id":                      PingResponse,
			"FAILED_AFTER_GETQID":                  MissingDataResponse,
		},
	}
	return &m
}

// succeededExec returns a successful GetQueryExecution response pointing at the
// S3 location where the query results CSV lives (key == query ID).
func succeededExec(id string) *athena.GetQueryExecutionOutput {
	loc := "s3://athena-results/" + id
	return &athena.GetQueryExecutionOutput{
		QueryExecution: &types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status: &types.QueryExecutionStatus{
				State: types.QueryExecutionStateSucceeded,
			},
			ResultConfiguration: &types.ResultConfiguration{
				OutputLocation: &loc,
			},
			StatementType: types.StatementTypeDml,
		},
	}
}

// GetQueryResults is a mock against athenaClient.GetQueryResults().
func (m *mockAthenaClient) GetQueryResults(ctx context.Context,
	query *athena.GetQueryResultsInput, _ ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error) {
	var nextToken = ""
	if query.NextToken != nil {
		nextToken = *query.NextToken
	}
	if *query.QueryExecutionId == "GetQueryResultsWithContext_return_error" {
		return nil, ErrTestMockGeneric
	}
	if nextToken == "GetQueryResultsWithContext_return_error" {
		return nil, ErrTestMockGeneric
	}
	gen, ok := m.queryToResultsGenMap[*query.QueryExecutionId]
	if !ok {
		return nil, ErrTestMockGeneric
	}
	return gen(nextToken)
}

func (m *mockAthenaClient) GetWorkGroup(ctx context.Context, gwi *athena.GetWorkGroupInput,
	_ ...func(*athena.Options)) (*athena.GetWorkGroupOutput, error) {
	if !m.GetWGStatus {
		// Mirror the "WorkGroup is not found." error the driver special-cases,
		// as a v2 smithy.APIError.
		return nil, &smithy.GenericAPIError{
			Code:    "InvalidRequestException",
			Message: "WorkGroup is not found.",
		}
	}
	state := types.WorkGroupStateEnabled
	if m.WGDisabled {
		state = types.WorkGroupStateDisabled
	}
	return &athena.GetWorkGroupOutput{
		WorkGroup: &types.WorkGroup{State: state},
	}, nil
}

func (m *mockAthenaClient) CreateWorkGroup(ctx context.Context, _ *athena.CreateWorkGroupInput,
	_ ...func(*athena.Options)) (*athena.CreateWorkGroupOutput, error) {
	if !m.CreateWGStatus {
		return nil, ErrTestMockGeneric
	}
	return &athena.CreateWorkGroupOutput{}, nil
}

func (m *mockAthenaClient) GetQueryRuntimeStatistics(ctx context.Context, _ *athena.GetQueryRuntimeStatisticsInput,
	_ ...func(*athena.Options)) (*athena.GetQueryRuntimeStatisticsOutput, error) {
	return &athena.GetQueryRuntimeStatisticsOutput{}, nil
}

func (m *mockAthenaClient) StartQueryExecution(ctx context.Context, s *athena.StartQueryExecutionInput,
	_ ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error) {
	newQID := func(qid string) *athena.StartQueryExecutionOutput {
		return &athena.StartQueryExecutionOutput{QueryExecutionId: &qid}
	}
	if strings.ToLower(*s.QueryString) == "select 1" { // Ping
		return newQID("PING_OK_QID"), nil
	}
	if *s.QueryString == "SELECTExecContext_OK" {
		return newQID("SELECTExecContext_OK_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_OK" ||
		*s.QueryString == "SELECTQueryContext_'OK'" ||
		*s.QueryString == "SELECTQueryContext_?" {
		return newQID("SELECTQueryContext_OK_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_CANCEL_OK" {
		return newQID("SELECTQueryContext_CANCEL_OK_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_AWS_CANCEL" {
		return newQID("SELECTQueryContext_AWS_CANCEL_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_AWS_FAIL" {
		return newQID("SELECTQueryContext_AWS_FAIL_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_CANCEL_FAIL" {
		return newQID("SELECTQueryContext_CANCEL_FAIL_QID"), nil
	}
	if *s.QueryString == "SELECTQueryContext_TIMEOUT" {
		return newQID("SELECTQueryContext_TIMEOUT_QID"), nil
	}
	if *s.QueryString == "StartQueryExecution_nil_error" {
		return nil, ErrTestMockGeneric
	}
	if *s.QueryString == "When_StartQueryExecution_Succeed_but_GetQueryExecutionWithContext_return_nil_and_error" {
		return newQID("When_StartQueryExecution_Succeed_but_GetQueryExecutionWithContext_return_nil_and_error_QID"), nil
	}
	if *s.QueryString == "StartQueryExecution_OK_GetQueryExecutionWithContext_QueryExecutionStateCancelled" {
		return newQID("QueryExecutionStateCancelled_QID"), nil
	}
	if *s.QueryString == "StartQueryExecution_OK_GetQueryExecutionWithContext_QueryExecutionStateFailed" {
		return newQID("QueryExecutionStateFailed_QID"), nil
	}
	if *s.QueryString == "FAILED_AFTER_GETQID" {
		qid := "FAILED_AFTER_GETQID_123"
		return newQID(qid), fmt.Errorf("FAILED_AFTER_GETQID_FAILED")
	}
	if *s.QueryString == "FAILED_AFTER_GETQID2" {
		qid := "FAILED_AFTER_GETQID_123"
		// A v2 HTTP response error carrying a request ID; the driver's PCGetQID
		// path extracts it via awshttp.ResponseError.ServiceRequestID().
		respErr := &awshttp.ResponseError{
			ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}},
				Err:      fmt.Errorf("FAILED_AFTER_GETQID_FAILED"),
			},
			RequestID: qid,
		}
		return newQID(qid), respErr
	}
	return nil, nil
}

func (m *mockAthenaClient) GetQueryExecution(ctx context.Context,
	input *athena.GetQueryExecutionInput, _ ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error) {
	id := *input.QueryExecutionId
	newExec := func(exec *types.QueryExecution) *athena.GetQueryExecutionOutput {
		return &athena.GetQueryExecutionOutput{QueryExecution: exec}
	}
	dataScanned := int64(123)
	switch id {
	case "When_StartQueryExecution_Succeed_but_GetQueryExecutionWithContext_return_nil_and_error_QID":
		return nil, ErrTestMockGeneric
	case "QueryExecutionStateCancelled_QID":
		return nil, context.Canceled
	case "QueryExecutionStateFailed_QID":
		return nil, ErrTestMockFailedByAthena
	case "PING_OK_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateSucceeded},
		}), nil
	case "SELECTExecContext_OK_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateSucceeded},
			Statistics:       &types.QueryExecutionStatistics{DataScannedInBytes: &dataScanned},
		}), nil
	case "SELECTQueryContext_OK_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateSucceeded},
			StatementType:    types.StatementTypeDdl,
		}), nil
	case "SELECTQueryContext_CANCEL_OK_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateQueued},
			StatementType:    types.StatementTypeDdl,
			Statistics:       &types.QueryExecutionStatistics{DataScannedInBytes: &dataScanned},
		}), nil
	case "SELECTQueryContext_AWS_CANCEL_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateCancelled},
			Statistics:       &types.QueryExecutionStatistics{DataScannedInBytes: &dataScanned},
		}), nil
	case "SELECTQueryContext_AWS_FAIL_QID":
		reason := "something_broken"
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status: &types.QueryExecutionStatus{
				State:             types.QueryExecutionStateFailed,
				StateChangeReason: &reason,
			},
		}), nil
	case "SELECTQueryContext_CANCEL_FAIL_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateQueued},
		}), nil
	case "SELECTQueryContext_TIMEOUT_QID":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateQueued},
			StatementType:    types.StatementType("TIMEOUT_NOW"),
		}), nil
	case "c89088ab-595d-4ee6-a9ce-73b55aeb8900":
		return newExec(&types.QueryExecution{
			Query:            &id,
			QueryExecutionId: &id,
			Status:           &types.QueryExecutionStatus{State: types.QueryExecutionStateQueued},
			StatementType:    types.StatementTypeDdl,
			Statistics:       &types.QueryExecutionStatistics{DataScannedInBytes: &dataScanned},
		}), nil
	}
	if newRowsSuccessIDs[id] {
		return succeededExec(id), nil
	}
	return nil, ErrTestMockGeneric
}

func (m *mockAthenaClient) StopQueryExecution(ctx context.Context, input *athena.StopQueryExecutionInput,
	_ ...func(*athena.Options)) (*athena.StopQueryExecutionOutput, error) {
	switch *input.QueryExecutionId {
	case "SELECTQueryContext_CANCEL_OK_QID":
		return &athena.StopQueryExecutionOutput{}, nil
	case "SELECTQueryContext_CANCEL_FAIL_QID":
		return nil, ErrTestMockGeneric
	case "c89088ab-595d-4ee6-a9ce-73b55aeb8954":
		return &athena.StopQueryExecutionOutput{}, nil
	case "c89088ab-595d-4ee6-a9ce-73b55aeb8955":
		return nil, ErrTestMockGeneric
	}
	return nil, ErrTestMockGeneric
}

// mockS3Client serves query-result CSVs from the csvFixtures registry, keyed by
// the S3 object key (which equals the query ID, per succeededExec).
type mockS3Client struct{}

func (m *mockS3Client) GetObject(ctx context.Context, in *s3.GetObjectInput,
	_ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := ""
	if in.Key != nil {
		key = *in.Key
	}
	if f, ok := csvFixtures[key]; ok {
		return &s3.GetObjectOutput{Body: f()}, nil
	}
	return nil, ErrTestMockGeneric
}

// mockDownloader serves the same csvFixtures registry as mockS3Client. It must
// actually write the fixture: GetScratchDir falls back to os.TempDir, so the
// background download in openResults runs in every test, and its result
// replaces the active CSV reader mid-read. A no-op "success" here would swap in
// an empty file and truncate result sets depending on goroutine scheduling.
type mockDownloader struct{}

func (m *mockDownloader) Download(ctx context.Context, w io.WriterAt, in *s3.GetObjectInput,
	_ ...func(*manager.Downloader)) (int64, error) {
	key := ""
	if in.Key != nil {
		key = *in.Key
	}
	f, ok := csvFixtures[key]
	if !ok {
		return 0, ErrTestMockGeneric
	}
	body, err := io.ReadAll(f())
	if err != nil {
		return 0, err
	}
	n, err := w.WriteAt(body, 0)
	return int64(n), err
}

// errAfterReader serves data then returns err once drained, letting tests
// inject a read error partway through streaming a result-set CSV.
type errAfterReader struct {
	data []byte
	off  int
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

func (r *errAfterReader) Close() error { return nil }

// csvFixtures maps a query ID to a factory that produces the result-set CSV body
// for that query. Each NewRows opens the body once; tests may open the same
// query multiple times, so factories return a fresh reader per call.
var csvFixtures = map[string]func() io.ReadCloser{}

func staticCSV(s string) func() io.ReadCloser {
	return func() io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
}

func errCSV(s string, err error) func() io.ReadCloser {
	return func() io.ReadCloser { return &errAfterReader{data: []byte(s), err: err} }
}

// sampleCSVValue returns a valid CSV cell for the given Athena column type.
func sampleCSVValue(colType string) string {
	switch colType {
	case "boolean":
		return "true"
	case "tinyint", "smallint", "integer", "bigint":
		return "42"
	case "float", "real", "double":
		return "1.5"
	case "date", "time", "time with time zone", "timestamp", "timestamp with time zone":
		return "2020-01-20"
	default:
		return "x"
	}
}

// csvBody renders a header row of column names followed by rowCount data rows of
// type-appropriate values.
func csvBody(columns []types.ColumnInfo, rowCount int) string {
	var b strings.Builder
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = *c.Name
	}
	b.WriteString(strings.Join(names, ",") + "\n")
	for r := 0; r < rowCount; r++ {
		cells := make([]string, len(columns))
		for i, c := range columns {
			t := ""
			if c.Type != nil {
				t = *c.Type
			}
			cells[i] = sampleCSVValue(t)
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
	return b.String()
}

func init() {
	cols := createTestColumns()
	csvFixtures["SELECT_OK"] = staticCSV(csvBody(cols, 35))
	csvFixtures["SELECT_EMPTY_ROW_IN_PAGE"] = staticCSV(csvBody(cols, 5))
	csvFixtures["show"] = staticCSV(csvBody([]types.ColumnInfo{newColumnInfo("partition", "string")}, 5))
	csvFixtures["RowsNextFailed"] = errCSV(csvBody(cols, 4), ErrTestMockGeneric)
	csvFixtures["SELECT_GetQueryResults_ERR"] = errCSV(csvBody(cols, 8), ErrTestMockGeneric)
	// A single quoted-empty field is a non-blank line, so the CSV reader yields
	// one empty-valued record (a blank line would be skipped).
	csvFixtures["missing_data_resp"] = staticCSV("c1\n\"\"\n")
	csvFixtures["missing_data_resp2"] = staticCSV("c2\n\"\"\n")
}

func MultiplePagesQueryResponse(token string) (*athena.GetQueryResultsOutput, error) {
	columns := createTestColumns()
	return newRandomHeaderResultPage(columns, nil, 6), nil
}

func ShowResponse(_ string) (*athena.GetQueryResultsOutput, error) {
	columns := []types.ColumnInfo{
		newColumnInfo("partition", "string"),
	}
	return newRandomHeaderResultPage(columns, nil, 6), nil
}

func OneColumnZeroRowResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c := newColumnInfo("a", nil)
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c},
				},
			},
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func OneColumnZeroRowResponseValid(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c := newColumnInfo("rows", nil)
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c},
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func ColumnMoreThanRowFieldResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c1 := newColumnInfo("c1", nil)
		c2 := newColumnInfo("c2", nil)
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c1, c2},
				},
				Rows: []types.Row{
					randRow([]types.ColumnInfo{c1}),
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func RowFieldMoreThanColumnsResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c1 := newColumnInfo("c1", nil)
		c2 := newColumnInfo("c2", nil)
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c1},
				},
				Rows: []types.Row{
					randRow([]types.ColumnInfo{c1, c2}),
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func MissingDataResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c1 := newColumnInfo("c1", "integer")
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c1},
				},
				Rows: []types.Row{
					missingDataRow([]types.ColumnInfo{c1}),
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func headPageWithColumnButNoRowResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c2 := newColumnInfo("c2", "string")
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c2},
				},
				Rows: []types.Row{
					missingDataRow([]types.ColumnInfo{c2}),
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func PingResponse(token string) (*athena.GetQueryResultsOutput, error) {
	switch token {
	case "":
		c2 := newColumnInfo("_col0", "integer")
		var i int64 = 1024
		return &athena.GetQueryResultsOutput{
			ResultSet: &types.ResultSet{
				ResultSetMetadata: &types.ResultSetMetadata{
					ColumnInfo: []types.ColumnInfo{c2},
				},
				Rows: []types.Row{
					randRow([]types.ColumnInfo{c2}),
				},
			},
			UpdateCount: &i,
		}, nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func NextFailedResponse(token string) (*athena.GetQueryResultsOutput, error) {
	columns := createTestColumns()
	switch token {
	case "":
		return newRandomHeaderResultPage(columns, nil, 5), nil
	default:
		return nil, ErrTestMockGeneric
	}
}

func createTestColumns() []types.ColumnInfo {
	return []types.ColumnInfo{
		newColumnInfo("test_array", "array"),
		newColumnInfo("active", "boolean"),
		newColumnInfo("company_name", "string"),
		newColumnInfo("project", "string"),
		newColumnInfo("uid", "integer"),
		newColumnInfo("regitser_date", "date"),
		newColumnInfo("regitser_ts", "timestamp"),
	}
}
