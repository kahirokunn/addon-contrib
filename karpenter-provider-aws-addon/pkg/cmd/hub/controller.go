package hub

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/utils/clock"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/hub"
	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/version"

	commonoptions "open-cluster-management.io/ocm/pkg/common/options"
)

func NewController() *cobra.Command {
	opts := commonoptions.NewOptions()
	cmdConfig := opts.NewControllerCommandConfig(
		"karpenter-provider-aws-addon-controller",
		version.Get(),
		hub.RunControllerManager,
		clock.RealClock{},
	)
	cmd := cmdConfig.NewCommandWithContext(context.Background())
	cmd.Use = "hub"
	cmd.Short = "Start the Karpenter Provider AWS add-on hub controller"

	opts.AddFlags(cmd.Flags())
	return cmd
}
