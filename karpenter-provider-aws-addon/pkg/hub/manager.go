package hub

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"k8s.io/client-go/dynamic"
	kubernetes "k8s.io/client-go/kubernetes"
	addonclientset "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	workclientset "open-cluster-management.io/api/client/work/clientset/versioned"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/hub/controllers/msasecretsync"
)

func RunControllerManager(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	kubeConfig := controllerContext.KubeConfig
	clusterClient, err := clusterclientset.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create cluster client: %w", err)
	}
	addonClient, err := addonclientset.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create addon client: %w", err)
	}
	workClient, err := workclientset.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create work client: %w", err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	return msasecretsync.Run(ctx, msasecretsync.RunOptions{
		KubeClient:    kubeClient,
		AddonClient:   addonClient,
		ClusterClient: clusterClient,
		DynamicClient: dynamicClient,
		WorkClient:    workClient,
	})
}
