package msasecretsync

import (
	"fmt"

	"k8s.io/client-go/tools/cache"
	workv1 "open-cluster-management.io/api/work/v1"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
)

const indexByTargetCluster = "karpenter-msa.target-cluster"

func indexManifestWorkByTargetCluster(obj interface{}) ([]string, error) {
	work, ok := obj.(*workv1.ManifestWork)
	if !ok {
		return nil, fmt.Errorf("unexpected ManifestWork cache object %T", obj)
	}
	target := work.Labels[addon.TargetClusterLabelKey]
	if target == "" {
		return nil, nil
	}
	return []string{target}, nil
}

func manifestWorkIndexers() cache.Indexers {
	return cache.Indexers{
		indexByTargetCluster: indexManifestWorkByTargetCluster,
	}
}
