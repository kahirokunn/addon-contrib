package main

import (
	goflag "flag"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"

	hubcmd "open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/cmd/hub"
	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/version"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	if err := newControllerCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newControllerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "karpenter-provider-aws-addon-controller",
		Short: "Karpenter Provider AWS Add-On Controller",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		},
	}
	cmd.Version = version.Get().String()
	cmd.AddCommand(hubcmd.NewController())
	return cmd
}
