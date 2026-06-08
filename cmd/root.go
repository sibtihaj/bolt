package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/config"
)

var cfgFile string
var globalConfig *config.TFEConfig

var rootCmd = &cobra.Command{
	Use:   "bolt",
	Short: "bolt — provision and manage Terraform Enterprise environments",
	Long: `bolt provisions Terraform Enterprise on Kubernetes (EKS, AKS, GKE, kubeadm)
and Docker with a single command, and tears them down just as easily.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.bolt/config.yaml)")
}

func initConfig() {
	path := cfgFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + "/.bolt/config.yaml"
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load config %s: %v\n", path, err)
		cfg = &config.TFEConfig{}
	}
	globalConfig = cfg
}
