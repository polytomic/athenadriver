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

package main

import (
	"database/sql"
	"fmt"

	secret "github.com/uber/athenadriver/examples/constants"
	drv "github.com/uber/athenadriver/go"
)

// useAssumeRoleWithExternalID demonstrates how to use IAM role assumption with an external ID
func useAssumeRoleWithExternalID() {
	// 1. Create a new config with base credentials
	conf, err := drv.NewDefaultConfig(secret.OutputBucketDev, secret.Region,
		secret.AccessID, secret.SecretAccessKey)
	if err != nil {
		fmt.Println("Error creating config:", err)
		return
	}

	// 2. Configure the role to assume
	conf.SetRoleArn("arn:aws:iam::123456789012:role/AthenaAccessRole")
	conf.SetExternalID("my-external-id-12345")
	conf.SetRoleSessionName("athena-query-session")

	// 3. Add session tags for ABAC (Attribute-Based Access Control)
	conf.SetSessionTag("Environment", "Production")
	conf.SetSessionTag("Team", "DataScience")
	conf.SetSessionTag("CostCenter", "12345")

	// 4. Open Connection - the driver will automatically assume the role
	db, err := sql.Open(drv.DriverName, conf.Stringify())
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}
	defer db.Close()

	// 5. Query and print results
	var i int
	err = db.QueryRow("SELECT 123").Scan(&i)
	if err != nil {
		fmt.Println("Error executing query:", err)
		return
	}
	fmt.Println("Query result with assumed role:", i)
}

// useAssumeRoleWithoutExternalID demonstrates role assumption without an external ID
func useAssumeRoleWithoutExternalID() {
	// 1. Create a new config
	conf, err := drv.NewDefaultConfig(secret.OutputBucketDev, secret.Region,
		secret.AccessID, secret.SecretAccessKey)
	if err != nil {
		fmt.Println("Error creating config:", err)
		return
	}

	// 2. Configure the role to assume (no external ID required)
	conf.SetRoleArn("arn:aws:iam::123456789012:role/AthenaAccessRole")
	// Session name is optional - defaults to "athenadriver-session" if not set

	// 3. Open Connection
	db, err := sql.Open(drv.DriverName, conf.Stringify())
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}
	defer db.Close()

	// 4. Query and print results
	var i int
	err = db.QueryRow("SELECT 456").Scan(&i)
	if err != nil {
		fmt.Println("Error executing query:", err)
		return
	}
	fmt.Println("Query result with assumed role (no external ID):", i)
}

// useAssumeRoleFromDSN demonstrates configuring role assumption via DSN string
func useAssumeRoleFromDSN() {
	// Create DSN with role assumption parameters
	dsn := "s3://my-bucket/results/?region=us-east-1&db=default" +
		"&roleArn=arn:aws:iam::123456789012:role/AthenaAccessRole" +
		"&externalID=my-external-id" +
		"&roleSessionName=my-session" +
		"&accessID=" + secret.AccessID +
		"&secretAccessKey=" + secret.SecretAccessKey

	// Open Connection using DSN
	db, err := sql.Open(drv.DriverName, dsn)
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}
	defer db.Close()

	// Query and print results
	var i int
	err = db.QueryRow("SELECT 789").Scan(&i)
	if err != nil {
		fmt.Println("Error executing query:", err)
		return
	}
	fmt.Println("Query result with assumed role (from DSN):", i)
}

func main() {
	fmt.Println("=== IAM Role Assumption Examples ===")
	fmt.Println("\nExample 1: Assume role with external ID")
	useAssumeRoleWithExternalID()

	fmt.Println("\nExample 2: Assume role without external ID")
	useAssumeRoleWithoutExternalID()

	fmt.Println("\nExample 3: Assume role from DSN")
	useAssumeRoleFromDSN()
}

/*
Sample Output:
=== IAM Role Assumption Examples ===

Example 1: Assume role with external ID
Query result with assumed role: 123

Example 2: Assume role without external ID
Query result with assumed role (no external ID): 456

Example 3: Assume role from DSN
Query result with assumed role (from DSN): 789
*/
