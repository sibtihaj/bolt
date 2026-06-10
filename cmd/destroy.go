package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/infra"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
	"github.com/spf13/cobra"
)

var destroyOpts struct {
	Name  string
	Force bool
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy a TFE deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := state.Load(destroyOpts.Name)
		if err != nil {
			return fmt.Errorf("deployment %q not found: %w", destroyOpts.Name, err)
		}

		// If bolt provisioned cloud infrastructure, tear it down first.
		if d.InfraState != nil && d.InfraState.Cloud != "" {
			if err := handleInfraDestroy(d); err != nil {
				if !destroyOpts.Force {
					return err
				}
				fmt.Printf("\nWarning: infra teardown had errors (continuing with --force):\n%s\n\n", err)
			}
		}

		p, err := tfe.NewProvisioner(d)
		if err != nil {
			return err
		}
		return p.Destroy(destroyOpts.Force)
	},
}

// handleInfraDestroy prompts for confirmation and cloud credentials, then
// runs infra.Destroy for the resources bolt provisioned.
func handleInfraDestroy(d *state.TFEDeployment) error {
	st := d.InfraState
	summary := buildInfraDestroyPlan(st)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(cyanBright).Render("  Cloud Infrastructure to be destroyed:"))
	for _, line := range summary {
		fmt.Println("    " + line)
	}
	fmt.Println()

	var confirmed bool
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Permanently destroy %d cloud resource(s) above?", len(summary))).
				Affirmative("Yes, destroy").
				Negative("Cancel").
				Value(&confirmed),
		),
	).WithTheme(boltTheme()).Run(); err != nil || !confirmed {
		if err != nil {
			return err
		}
		return fmt.Errorf("destroy cancelled")
	}

	cfg, err := collectDestroyCredentials(st)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(cyanBright).Render("  Tearing down cloud infrastructure…"))
	return infra.Destroy(context.Background(), cfg, st)
}

// buildInfraDestroyPlan returns a human-readable list of resources bolt will delete.
func buildInfraDestroyPlan(st *state.InfraState) []string {
	var lines []string
	add := func(label, id string) {
		if id != "" {
			lines = append(lines, fmt.Sprintf("[%s]  %s", strings.ToUpper(st.Cloud), label+": "+id))
		}
	}
	add("VPC", st.VPCID)
	add("EKS Cluster", st.EKSClusterCreated)
	add("RDS Instance", st.RDSInstanceID)
	add("S3 Bucket", st.S3BucketCreated)
	add("AKS Cluster", st.AKSClusterCreated)
	add("Azure PostgreSQL", st.AzurePostgresServer)
	add("Azure Storage Account", st.AzureStorageAccount)
	add("GKE Cluster", st.GKEClusterCreated)
	add("Cloud SQL", st.CloudSQLInstanceID)
	add("GCS Bucket", st.GCSBucketCreated)
	if st.DatabaseChoice == state.DBInCluster {
		lines = append(lines, fmt.Sprintf("[%s]  In-cluster PostgreSQL StatefulSet", strings.ToUpper(st.Cloud)))
	}
	return lines
}

// collectDestroyCredentials re-prompts for cloud credentials; they are never
// stored, so the user must supply them again at destroy time.
func collectDestroyCredentials(st *state.InfraState) (*infra.DestroyConfig, error) {
	cfg := &infra.DestroyConfig{Cloud: st.Cloud}

	fmt.Println(lipgloss.NewStyle().Foreground(cyanBright).Render("  Re-enter cloud credentials to authenticate the destroy operation."))
	fmt.Println()

	switch st.Cloud {
	case "aws":
		awsCfg, _, err := interactiveAWSCredentials()
		if err != nil {
			return nil, err
		}
		cfg.AWS = &infra.AWSCreds{
			AssumeRoleARN:   awsCfg.AssumeRoleARN,
			Region:          awsCfg.Region,
			AccessKeyID:     awsCfg.AccessKeyID,
			SecretAccessKey: awsCfg.SecretAccessKey,
			SessionToken:    awsCfg.SessionToken,
		}

	case "azure":
		azCfg, err := interactiveAzureCredentials()
		if err != nil {
			return nil, err
		}
		cfg.Azure = &infra.AzureCreds{
			SubscriptionID: azCfg.SubscriptionID,
			TenantID:       azCfg.TenantID,
			ClientID:       azCfg.ClientID,
			ClientSecret:   azCfg.ClientSecret,
			ResourceGroup:  azCfg.ResourceGroup,
			Location:       azCfg.Location,
		}

	case "gcp":
		gcpCfg, err := interactiveGCPCredentials()
		if err != nil {
			return nil, err
		}
		cfg.GCP = &infra.GCPCreds{
			ProjectID:       gcpCfg.ProjectID,
			Region:          gcpCfg.Region,
			Zone:            gcpCfg.Zone,
			ServiceAcctJSON: gcpCfg.ServiceAcctJSON,
		}

	default:
		return nil, fmt.Errorf("unknown cloud provider %q in state", st.Cloud)
	}

	return cfg, nil
}

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().StringVarP(&destroyOpts.Name, "name", "n", "", "deployment name (required)")
	destroyCmd.Flags().BoolVarP(&destroyOpts.Force, "force", "f", false, "force destroy even if errors occur")
	_ = destroyCmd.MarkFlagRequired("name")
}
