# IAM Role Assumption with External ID and Session Tags

This document explains how to use IAM role assumption with athenadriver, including support for external IDs and session tags.

## Overview

The athenadriver now supports assuming IAM roles with optional external IDs and session tags. This is useful when:

- You need to access resources in a different AWS account
- You want to use temporary credentials with limited privileges
- You need to satisfy security requirements that mandate external IDs for cross-account access
- You want to implement least-privilege access patterns
- You need attribute-based access control (ABAC) using session tags

## Configuration

You can configure role assumption in two ways:

### 1. Using Config Methods (Recommended)

```go
import (
    "database/sql"
    drv "github.com/uber/athenadriver/go"
)

// Create base configuration
conf, err := drv.NewDefaultConfig(outputBucket, region, accessID, secretAccessKey)
if err != nil {
    // handle error
}

// Configure role assumption
conf.SetRoleArn("arn:aws:iam::123456789012:role/AthenaAccessRole")
conf.SetExternalID("my-external-id-12345")  // Optional
conf.SetRoleSessionName("my-session-name")   // Optional, defaults to "athenadriver-session"

// Add session tags (optional, for ABAC)
conf.SetSessionTag("Environment", "Production")
conf.SetSessionTag("Team", "DataScience")

// Open connection
db, err := sql.Open(drv.DriverName, conf.Stringify())
```

### 2. Using DSN String

```go
dsn := "s3://my-bucket/results/?region=us-east-1&db=default" +
    "&roleArn=arn:aws:iam::123456789012:role/AthenaAccessRole" +
    "&externalID=my-external-id" +
    "&roleSessionName=my-session" +
    "&sessionTags=Environment`Production|Team`DataScience" +
    "&accessID=YOUR_ACCESS_KEY" +
    "&secretAccessKey=YOUR_SECRET_KEY"

db, err := sql.Open(drv.DriverName, dsn)
```

## Configuration Parameters

| Parameter | Config Method | DSN Query Param | Environment Variable | Required | Default |
|-----------|--------------|-----------------|---------------------|----------|---------|
| Role ARN | `SetRoleArn()` | `roleArn` | `AWS_ROLE_ARN` | Yes (for role assumption) | - |
| External ID | `SetExternalID()` | `externalID` | `AWS_EXTERNAL_ID` | No | - |
| Session Name | `SetRoleSessionName()` | `roleSessionName` | `AWS_ROLE_SESSION_NAME` | No | `athenadriver-session` |
| Session Tags | `SetSessionTag(key, value)` | `sessionTags` | `AWS_SESSION_TAGS` | No | - |

**Note:** Session tags in DSN use the format `key1`value1|key2`value2`. Use `SetSessionTag()` multiple times to add multiple tags programmatically.

## Authentication Order

When connecting, the driver tries authentication methods in this order:

1. **AWS Profile** - If `AWS_SDK_LOAD_CONFIG=1` and profile is set
2. **IAM Role Assumption** - If `roleArn` is configured
3. **Static Credentials** - If `accessID` is provided
4. **Default Credential Chain** - Environment variables, EC2 instance profile, etc.

## Use Cases

### Cross-Account Access with External ID

When a third party needs to access your AWS resources, you can require an external ID for additional security:

```go
conf.SetRoleArn("arn:aws:iam::123456789012:role/ThirdPartyAccess")
conf.SetExternalID("unique-id-provided-by-third-party")
```

### Assuming a Role from EC2 Instance

If running on EC2 with an instance profile, you can assume a different role:

```go
// Create config without static credentials
conf := drv.NewNoOpsConfig()
conf.SetOutputBucket("s3://my-bucket/")
conf.SetRegion("us-east-1")

// Assume a different role
conf.SetRoleArn("arn:aws:iam::123456789012:role/DataScientistRole")

db, err := sql.Open(drv.DriverName, conf.Stringify())
```

### Temporary Elevated Privileges

Assume a role with elevated permissions for specific operations:

```go
conf.SetRoleArn("arn:aws:iam::123456789012:role/AthenaAdminRole")
conf.SetRoleSessionName("maintenance-session-" + timestamp)

db, err := sql.Open(drv.DriverName, conf.Stringify())
// Perform admin operations
db.Close()
```

### Attribute-Based Access Control (ABAC) with Session Tags

Use session tags to implement fine-grained access control based on attributes:

```go
conf.SetRoleArn("arn:aws:iam::123456789012:role/AthenaAccessRole")

// Add session tags that can be used in IAM policies
conf.SetSessionTag("Environment", "Production")
conf.SetSessionTag("Team", "DataScience")
conf.SetSessionTag("CostCenter", "CC-12345")
conf.SetSessionTag("Project", "CustomerAnalytics")

db, err := sql.Open(drv.DriverName, conf.Stringify())
```

Session tags can be used in IAM policies to control access:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "athena:*",
    "Resource": "*",
    "Condition": {
      "StringEquals": {
        "aws:PrincipalTag/Environment": "${aws:RequestTag/Environment}",
        "aws:PrincipalTag/Team": "${aws:RequestTag/Team}"
      }
    }
  }]
}
```

## Examples

Complete working examples are available in:
- [examples/assume_role.go](examples/assume_role.go)

## Security Best Practices

1. **Use External IDs** - Always use external IDs for cross-account access to prevent the "confused deputy" problem
2. **Session Names** - Use descriptive session names to help with auditing and CloudTrail logs
3. **Session Tags** - Use session tags for ABAC to enforce fine-grained access control based on user attributes
4. **Least Privilege** - Configure assumed roles with minimal required permissions
5. **Credential Management** - Never hardcode credentials; use environment variables or AWS credential providers
6. **Session Duration** - The AWS SDK automatically handles credential refresh for long-running applications
7. **Tag Validation** - Validate session tags to ensure they contain only allowed characters and values

## Troubleshooting

### Common Errors

**Error: "AccessDenied"**
- Verify the role ARN is correct
- Check that the base credentials have `sts:AssumeRole` permission
- Ensure the role's trust policy allows your principal to assume it

**Error: "InvalidIdentityToken"**
- Verify the external ID matches what's required in the role's trust policy
- Check for typos in the external ID

**Error: "InvalidParameterValue"**
- Ensure the role ARN format is correct: `arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME`
- Verify the session name contains only allowed characters (alphanumeric, `=,.@-`)

### Debugging

Enable AWS SDK logging to see STS AssumeRole calls:

```go
import "github.com/aws/aws-sdk-go/aws"

// In your config setup
conf.SetLogging(true)
```

## IAM Policy Examples

### Trust Policy for Role (Required)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::999999999999:user/your-user"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "my-external-id-12345"
        }
      }
    }
  ]
}
```

### Policy for User Assuming Role (Required)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": "arn:aws:iam::123456789012:role/AthenaAccessRole"
    }
  ]
}
```

## Additional Resources

- [AWS STS AssumeRole Documentation](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)
- [How to Use External IDs](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create_for-user_externalid.html)
- [AWS SDK for Go Credentials](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html)
