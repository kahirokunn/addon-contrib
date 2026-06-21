package msasecretsync

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
)

func TestBuildManagedKubeconfigUsesTokenFile(t *testing.T) {
	kubeconfig, err := BuildManagedKubeconfig("https://target-api.example.com", "/managed/config/ca.crt", addon.ManagedTokenPath)
	if err != nil {
		t.Fatalf("BuildManagedKubeconfig() error = %v", err)
	}

	text := string(kubeconfig)
	if !strings.Contains(text, "tokenFile: "+addon.ManagedTokenPath) {
		t.Fatalf("kubeconfig does not reference tokenFile:\n%s", text)
	}
	if strings.Contains(text, "token: ") {
		t.Fatalf("kubeconfig must not embed token material:\n%s", text)
	}
	if !strings.Contains(text, "certificate-authority: /managed/config/ca.crt") {
		t.Fatalf("kubeconfig should reference mounted CA path:\n%s", text)
	}
}

func TestBuildHostingSecretManifestWorkSyncsTokenCAAndKubeconfig(t *testing.T) {
	mca := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      addon.AddonName,
			Namespace: "target-a",
			UID:       "12345",
			Annotations: map[string]string{
				addonv1beta1.HostingClusterNameAnnotationKey: "hosting-a",
			},
		},
	}
	cluster := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "target-a"},
		Spec: clusterv1.ManagedClusterSpec{
			ManagedClusterClientConfigs: []clusterv1.ClientConfig{{
				URL: "https://target-api.example.com",
			}},
		},
	}
	source := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: addon.AddonName, Namespace: "target-a"},
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey:  []byte("token-value"),
			corev1.ServiceAccountRootCAKey: []byte("ca-value"),
		},
	}

	work, err := BuildHostingSecretManifestWork(mca, cluster, "karpenter-system", source)
	if err != nil {
		t.Fatalf("BuildHostingSecretManifestWork() error = %v", err)
	}
	if work.Namespace != "hosting-a" {
		t.Fatalf("ManifestWork namespace = %q, want hosting-a", work.Namespace)
	}

	var secret *corev1.Secret
	for _, manifest := range work.Spec.Workload.Manifests {
		if object, ok := manifest.RawExtension.Object.(*corev1.Secret); ok {
			secret = object
			break
		}
	}
	if secret == nil {
		t.Fatalf("ManifestWork does not include Secret")
	}
	if secret.Name != addon.ManagedKubeconfigSecretName || secret.Namespace != "karpenter-system" {
		t.Fatalf("synced Secret = %s/%s", secret.Namespace, secret.Name)
	}
	for _, key := range []string{addon.ManagedKubeconfigSecretKey, corev1.ServiceAccountTokenKey, corev1.ServiceAccountRootCAKey} {
		if len(secret.Data[key]) == 0 {
			t.Fatalf("synced Secret missing data[%q]", key)
		}
	}
	if strings.Contains(string(secret.Data[addon.ManagedKubeconfigSecretKey]), "token: token-value") {
		t.Fatalf("synced kubeconfig embeds token material")
	}
}
