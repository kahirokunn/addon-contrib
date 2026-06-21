package msasecretsync

import (
	"context"

	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	addonclientset "open-cluster-management.io/api/client/addon/clientset/versioned"
	"open-cluster-management.io/sdk-go/pkg/patcher"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
)

func addonPatcher(client addonclientset.Interface, namespace string) patcher.Patcher[*addonv1beta1.ManagedClusterAddOn, addonv1beta1.ManagedClusterAddOnSpec, addonv1beta1.ManagedClusterAddOnStatus] {
	return patcher.NewPatcher[
		*addonv1beta1.ManagedClusterAddOn,
		addonv1beta1.ManagedClusterAddOnSpec,
		addonv1beta1.ManagedClusterAddOnStatus,
	](client.AddonV1beta1().ManagedClusterAddOns(namespace))
}

func ensureFinalizer(ctx context.Context, client addonclientset.Interface, mca *addonv1beta1.ManagedClusterAddOn) error {
	_, err := addonPatcher(client, mca.Namespace).AddFinalizer(ctx, mca, addon.Finalizer)
	return err
}

func removeFinalizer(ctx context.Context, client addonclientset.Interface, mca *addonv1beta1.ManagedClusterAddOn) error {
	return addonPatcher(client, mca.Namespace).RemoveFinalizer(ctx, mca, addon.Finalizer)
}
