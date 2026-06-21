package msasecretsync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
)

const managedContextName = "managed"

func BuildManagedKubeconfig(server, caPath, tokenPath string) ([]byte, error) {
	if server == "" {
		return nil, fmt.Errorf("managed cluster API server URL is required")
	}
	config := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			managedContextName: {
				Server:               server,
				CertificateAuthority: caPath,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			managedContextName: {
				TokenFile: tokenPath,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			managedContextName: {
				Cluster:  managedContextName,
				AuthInfo: managedContextName,
			},
		},
		CurrentContext: managedContextName,
	}
	return clientcmd.Write(config)
}

func BuildHostingSecretManifestWork(
	managedClusterAddOn *addonv1beta1.ManagedClusterAddOn,
	managedCluster *clusterv1.ManagedCluster,
	installNamespace string,
	source *corev1.Secret,
) (*workv1.ManifestWork, error) {
	hostingClusterName := managedClusterAddOn.Annotations[addonv1beta1.HostingClusterNameAnnotationKey]
	if hostingClusterName == "" {
		return nil, fmt.Errorf("ManagedClusterAddOn %s/%s requires annotation %s", managedClusterAddOn.Namespace, managedClusterAddOn.Name, addonv1beta1.HostingClusterNameAnnotationKey)
	}
	if len(managedCluster.Spec.ManagedClusterClientConfigs) == 0 || managedCluster.Spec.ManagedClusterClientConfigs[0].URL == "" {
		return nil, fmt.Errorf("ManagedCluster %s has no managedClusterClientConfigs URL", managedCluster.Name)
	}
	clientConfig := managedCluster.Spec.ManagedClusterClientConfigs[0]
	token := source.Data[corev1.ServiceAccountTokenKey]
	if len(token) == 0 {
		return nil, fmt.Errorf("MSA token Secret %s/%s missing data[%s]", source.Namespace, source.Name, corev1.ServiceAccountTokenKey)
	}
	ca := source.Data[corev1.ServiceAccountRootCAKey]
	if len(ca) == 0 {
		ca = clientConfig.CABundle
	}
	if len(ca) == 0 {
		return nil, fmt.Errorf("MSA token Secret %s/%s missing data[%s] and ManagedCluster has no CABundle", source.Namespace, source.Name, corev1.ServiceAccountRootCAKey)
	}

	kubeconfig, err := BuildManagedKubeconfig(clientConfig.URL, addon.ManagedCAPath, addon.ManagedTokenPath)
	if err != nil {
		return nil, err
	}

	clusterLabels := addon.ManagedClusterLabels(managedCluster.Name)
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   installNamespace,
			Labels: clusterLabels,
		},
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      addon.ManagedKubeconfigSecretName,
			Namespace: installNamespace,
			Labels:    clusterLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			addon.ManagedKubeconfigSecretKey: kubeconfig,
			corev1.ServiceAccountTokenKey:  token,
			corev1.ServiceAccountRootCAKey: ca,
		},
	}

	return &workv1.ManifestWork{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "work.open-cluster-management.io/v1",
			Kind:       "ManifestWork",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManifestWorkName(managedCluster.Name),
			Namespace: hostingClusterName,
			Labels:    clusterLabels,
		},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{RawExtension: runtime.RawExtension{Object: namespace}},
					{RawExtension: runtime.RawExtension{Object: secret}},
				},
			},
		},
	}, nil
}

func ManifestWorkName(managedClusterName string) string {
	return addon.AddonName + "-" + shortHash(managedClusterName) + "-managed-kubeconfig"
}

// shortHash truncates sha256 to 10 hex chars so the ManifestWork name stays
// well under the DNS-1123 253-char limit while remaining unique across
// realistic managed-cluster cardinalities.
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}
