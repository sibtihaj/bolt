package diagnostics

import (
	"fmt"
	"os"
	"time"

	runner "github.com/sibtihaj/bolt/internal/exec"
)

// CloudTrailEKSEvents queries AWS CloudTrail for recent EKS-related errors.
// Requires aws CLI with cloudtrail:LookupEvents permission.
func CloudTrailEKSEvents(region string) {
	if region == "" {
		return
	}
	fmt.Printf("\n─── AWS CloudTrail — EKS Errors (region: %s, last 30m) ───\n", region)
	startTime := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	_ = runner.Run("aws", []string{
		"cloudtrail", "lookup-events",
		"--lookup-attributes", "AttributeKey=EventSource,AttributeValue=eks.amazonaws.com",
		"--start-time", startTime,
		"--region", region,
		"--query", "Events[?ErrorCode!=null].[EventTime,EventName,ErrorCode,ErrorMessage]",
		"--output", "table",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}

// AzureActivityLogErrors queries the Azure Monitor Activity Log for recent
// failed operations in the given resource group.
// Requires az CLI and Monitor Reader permission.
func AzureActivityLogErrors(resourceGroup string) {
	if resourceGroup == "" {
		return
	}
	fmt.Printf("\n─── Azure Activity Log — Failed Events (rg: %s, last 30m) ───\n", resourceGroup)
	_ = runner.Run("az", []string{
		"monitor", "activity-log", "list",
		"--resource-group", resourceGroup,
		"--offset", "30m",
		"--status", "Failed",
		"--query", "[].{time:eventTimestamp,operation:operationName.localizedValue,status:status.localizedValue,message:properties.statusMessage}",
		"--output", "table",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}

// GCPClusterErrors queries GCP Cloud Logging for recent error-level events
// from the specified GKE cluster.
// Requires gcloud CLI with logging.read permission.
func GCPClusterErrors(project, clusterName string) {
	if project == "" || clusterName == "" {
		return
	}
	fmt.Printf("\n─── GCP Cloud Logging — GKE Errors (cluster: %s) ───\n", clusterName)
	filter := fmt.Sprintf(
		`resource.type="k8s_cluster" AND resource.labels.cluster_name="%s" AND severity>=ERROR`,
		clusterName,
	)
	_ = runner.Run("gcloud", []string{
		"logging", "read", filter,
		"--project", project,
		"--limit", "20",
		"--format", "table(timestamp,severity,textPayload,jsonPayload.message)",
	}, runner.RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
}
