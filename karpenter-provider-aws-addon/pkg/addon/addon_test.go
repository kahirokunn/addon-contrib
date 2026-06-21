package addon

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
)

func TestValuesFromDeploymentConfigRequiresStandardVariablesAndIgnoresAnnotations(t *testing.T) {
	mca := validManagedClusterAddOn()
	mca.Annotations[legacyAnnotation("aws-region")] = "annotation-region"
	mca.Annotations[legacyAnnotation("cluster-name")] = "annotation-cluster"
	mca.Annotations[legacyAnnotation("cluster-endpoint")] = "https://annotation-api.example.com"
	mca.Annotations[legacyAnnotation("node-role-arn")] = "arn:aws:iam::123456789012:role/AnnotationNodeRole"

	_, err := ValuesFromDeploymentConfig(mca, &addonv1beta1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: AddonName, Namespace: "target-a"},
		Spec: addonv1beta1.AddOnDeploymentConfigSpec{
			AgentInstallNamespace: "karpenter-system",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "AWS_REGION") {
		t.Fatalf("ValuesFromDeploymentConfig() error = %v, want missing AWS_REGION", err)
	}

	values, err := ValuesFromDeploymentConfig(mca, validAddOnDeploymentConfig())
	if err != nil {
		t.Fatalf("ValuesFromDeploymentConfig() error = %v", err)
	}

	if values.AgentInstallNamespace != "karpenter-system" {
		t.Fatalf("AgentInstallNamespace = %q, want karpenter-system", values.AgentInstallNamespace)
	}
	if values.AWSRegion != "us-east-1" {
		t.Fatalf("AWSRegion = %q, want config value and not annotation fallback", values.AWSRegion)
	}
	if values.KarpenterClusterName != "config-eks" {
		t.Fatalf("KarpenterClusterName = %q, want config-eks", values.KarpenterClusterName)
	}
	if values.ClusterEndpoint != "https://config-api.example.com" {
		t.Fatalf("ClusterEndpoint = %q, want config endpoint", values.ClusterEndpoint)
	}
	if values.NodeRoleARN != "arn:aws:iam::123456789012:role/ConfigNodeRole" {
		t.Fatalf("NodeRoleARN = %q, want config node role", values.NodeRoleARN)
	}
	if values.InterruptionQueue != "config-queue" {
		t.Fatalf("InterruptionQueue = %q, want config-queue", values.InterruptionQueue)
	}
}

func TestDefaultPrerequisiteObjectsTargetAddonPodServiceAccount(t *testing.T) {
	mca := validManagedClusterAddOn()
	delete(mca.Annotations, addonv1beta1.HostingClusterNameAnnotationKey)
	config := mustDeploymentConfigValues(t, mca, validAddOnDeploymentConfig())

	objects, err := BuildPrerequisiteObjects("target-a", mca, config, nil)
	if err != nil {
		t.Fatalf("BuildPrerequisiteObjects() error = %v", err)
	}

	assertNoUnstructuredKind(t, objects, "ManagedServiceAccount")

	clusterPermission := findUnstructured(t, objects, "rbac.open-cluster-management.io/v1alpha1", "ClusterPermission", AddonName)
	assertOwnedByAddon(t, clusterPermission, mca)
	assertClusterPermissionSubject(t, clusterPermission, map[string]string{
		"kind":      "ServiceAccount",
		"name":      ServiceAccountName,
		"namespace": "karpenter-system",
	})
	assertClusterPermissionDoesNotContain(t, clusterPermission, "authentication.k8s.io", "selfsubjectreviews", "create")
	assertClusterPermissionDoesNotContain(t, clusterPermission, "", "serviceaccounts", "get")
	assertClusterPermissionDoesNotContain(t, clusterPermission, "", "serviceaccounts/token", "create")
	assertClusterPermissionContains(t, clusterPermission, "storage.k8s.io", "storageclasses", "get")
	assertClusterPermissionContains(t, clusterPermission, "", "namespaces", "watch")
	assertClusterPermissionContains(t, clusterPermission, "", "pods", "delete")
	assertClusterPermissionRoleContains(t, clusterPermission, "karpenter-system", "coordination.k8s.io", "leases", "create")
	assertClusterPermissionRoleContains(t, clusterPermission, "kube-system", "", "services", "get")

	role := findUnstructured(t, objects, "aws.identity.appthrust.io/v1alpha1", "AWSServiceAccountRole", AddonName)
	assertOwnedByAddon(t, role, mca)
	assertAWSRoleServiceAccount(t, role, "karpenter-system", ServiceAccountName)
	if !policyStatementAllows(role, "iam:PassRole", "arn:aws:iam::123456789012:role/ConfigNodeRole") {
		t.Fatalf("AWSServiceAccountRole policyDocument does not scope iam:PassRole to the configured node role ARN")
	}
}

func TestHostedPrerequisiteObjectsUseMSASubjectAndMSAInstallNamespaceAWSRole(t *testing.T) {
	mca := validManagedClusterAddOn()
	config := mustDeploymentConfigValues(t, mca, validAddOnDeploymentConfig())
	managedServiceAccountAddOn := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManagedServiceAccountAddonName,
			Namespace: "target-a",
		},
		Status: addonv1beta1.ManagedClusterAddOnStatus{
			Namespace: "msa-runtime",
		},
	}

	objects, err := BuildPrerequisiteObjects("target-a", mca, config, managedServiceAccountAddOn)
	if err != nil {
		t.Fatalf("BuildPrerequisiteObjects() error = %v", err)
	}

	msa := findUnstructured(t, objects, "authentication.open-cluster-management.io/v1beta1", "ManagedServiceAccount", AddonName)
	if msa.GetNamespace() != "target-a" {
		t.Fatalf("ManagedServiceAccount namespace = %q, want target-a", msa.GetNamespace())
	}
	rotationEnabled, _, err := unstructured.NestedBool(msa.Object, "spec", "rotation", "enabled")
	if err != nil || !rotationEnabled {
		t.Fatalf("ManagedServiceAccount rotation enabled = %v, %v", rotationEnabled, err)
	}
	assertOwnedByAddon(t, msa, mca)

	clusterPermission := findUnstructured(t, objects, "rbac.open-cluster-management.io/v1alpha1", "ClusterPermission", AddonName)
	assertOwnedByAddon(t, clusterPermission, mca)
	assertClusterPermissionSubject(t, clusterPermission, map[string]string{
		"apiGroup": "authentication.open-cluster-management.io",
		"kind":     "ManagedServiceAccount",
		"name":     AddonName,
	})
	assertClusterPermissionRoleNamespace(t, clusterPermission, "msa-runtime")
	assertClusterPermissionContains(t, clusterPermission, "authentication.k8s.io", "selfsubjectreviews", "create")
	assertClusterPermissionContains(t, clusterPermission, "", "serviceaccounts", "get")
	assertClusterPermissionContains(t, clusterPermission, "", "serviceaccounts/token", "create")
	assertClusterPermissionContains(t, clusterPermission, "storage.k8s.io", "volumeattachments", "list")
	assertClusterPermissionContains(t, clusterPermission, "", "pods", "delete")
	assertClusterPermissionRoleContains(t, clusterPermission, "karpenter-system", "coordination.k8s.io", "leases", "update")
	assertClusterPermissionRoleContains(t, clusterPermission, "kube-system", "", "services", "get")

	role := findUnstructured(t, objects, "aws.identity.appthrust.io/v1alpha1", "AWSServiceAccountRole", AddonName)
	assertOwnedByAddon(t, role, mca)
	assertAWSRoleServiceAccount(t, role, "msa-runtime", ServiceAccountName)
	if !policyStatementAllows(role, "iam:PassRole", "arn:aws:iam::123456789012:role/ConfigNodeRole") {
		t.Fatalf("AWSServiceAccountRole policyDocument does not scope iam:PassRole to the configured node role ARN")
	}
}

func TestManagedServiceAccountInstallNamespaceFallsBackToAnnotationThenOCMDefault(t *testing.T) {
	withStatus := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				addonv1beta1.InstallNamespaceAnnotation: "annotation-ns",
			},
		},
		Status: addonv1beta1.ManagedClusterAddOnStatus{Namespace: "status-ns"},
	}
	if got := ManagedServiceAccountInstallNamespace(withStatus); got != "status-ns" {
		t.Fatalf("ManagedServiceAccountInstallNamespace(status) = %q, want status-ns", got)
	}

	withAnnotation := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				addonv1beta1.InstallNamespaceAnnotation: "annotation-ns",
			},
		},
	}
	if got := ManagedServiceAccountInstallNamespace(withAnnotation); got != "annotation-ns" {
		t.Fatalf("ManagedServiceAccountInstallNamespace(annotation) = %q, want annotation-ns", got)
	}

	if got := ManagedServiceAccountInstallNamespace(nil); got != DefaultManagedServiceAccountInstallNamespace {
		t.Fatalf("ManagedServiceAccountInstallNamespace(nil) = %q, want %q", got, DefaultManagedServiceAccountInstallNamespace)
	}
}

func validManagedClusterAddOn() *addonv1beta1.ManagedClusterAddOn {
	return &addonv1beta1.ManagedClusterAddOn{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "addon.open-cluster-management.io/v1beta1",
			Kind:       "ManagedClusterAddOn",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      AddonName,
			Namespace: "target-a",
			UID:       "12345",
			Annotations: map[string]string{
				addonv1beta1.HostingClusterNameAnnotationKey: "hosting-a",
			},
		},
	}
}

func legacyAnnotation(name string) string {
	return AnnotationPrefix + name
}

func validAddOnDeploymentConfig() *addonv1beta1.AddOnDeploymentConfig {
	return &addonv1beta1.AddOnDeploymentConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "addon.open-cluster-management.io/v1beta1",
			Kind:       "AddOnDeploymentConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      AddonName,
			Namespace: "target-a",
		},
		Spec: addonv1beta1.AddOnDeploymentConfigSpec{
			AgentInstallNamespace: "karpenter-system",
			CustomizedVariables: []addonv1beta1.CustomizedVariable{
				{Name: CustomizedVariableAWSRegion, Value: "us-east-1"},
				{Name: CustomizedVariableKarpenterClusterName, Value: "config-eks"},
				{Name: CustomizedVariableClusterEndpoint, Value: "https://config-api.example.com"},
				{Name: CustomizedVariableNodeRoleARN, Value: "arn:aws:iam::123456789012:role/ConfigNodeRole"},
				{Name: CustomizedVariableInterruptionQueue, Value: "config-queue"},
			},
		},
	}
}

func mustDeploymentConfigValues(
	t *testing.T,
	mca *addonv1beta1.ManagedClusterAddOn,
	config *addonv1beta1.AddOnDeploymentConfig,
) DeploymentConfigValues {
	t.Helper()
	values, err := ValuesFromDeploymentConfig(mca, config)
	if err != nil {
		t.Fatalf("ValuesFromDeploymentConfig() error = %v", err)
	}
	return values
}

func findUnstructured(t *testing.T, objects []*unstructured.Unstructured, apiVersion, kind, name string) *unstructured.Unstructured {
	t.Helper()
	for _, object := range objects {
		if object.GetAPIVersion() == apiVersion && object.GetKind() == kind && object.GetName() == name {
			return object
		}
	}
	t.Fatalf("%s %s %q not found", apiVersion, kind, name)
	return nil
}

func assertNoUnstructuredKind(t *testing.T, objects []*unstructured.Unstructured, kind string) {
	t.Helper()
	for _, object := range objects {
		if object.GetKind() == kind {
			t.Fatalf("%s should not be created", kind)
		}
	}
}

func assertOwnedByAddon(t *testing.T, object *unstructured.Unstructured, addon *addonv1beta1.ManagedClusterAddOn) {
	t.Helper()
	for _, owner := range object.GetOwnerReferences() {
		if owner.APIVersion == addon.APIVersion && owner.Kind == addon.Kind && owner.Name == addon.Name && owner.UID == addon.UID {
			return
		}
	}
	t.Fatalf("%s/%s ownerReferences = %#v, want ManagedClusterAddOn owner", object.GetKind(), object.GetName(), object.GetOwnerReferences())
}

func assertClusterPermissionSubject(t *testing.T, object *unstructured.Unstructured, want map[string]string) {
	t.Helper()
	subject, ok, err := unstructured.NestedStringMap(object.Object, "spec", "clusterRoleBinding", "subject")
	if err != nil || !ok {
		t.Fatalf("ClusterPermission subject missing: %v", err)
	}
	for key, value := range want {
		if subject[key] != value {
			t.Fatalf("ClusterPermission subject[%s] = %q, want %q (subject=%#v)", key, subject[key], value, subject)
		}
	}
}

func assertClusterPermissionRoleNamespace(t *testing.T, object *unstructured.Unstructured, want string) {
	t.Helper()
	roles, ok, err := unstructured.NestedSlice(object.Object, "spec", "roles")
	if err != nil || !ok || len(roles) == 0 {
		t.Fatalf("ClusterPermission roles missing: %v", err)
	}
	for _, item := range roles {
		role, _ := item.(map[string]interface{})
		if role["namespace"] == want {
			return
		}
	}
	t.Fatalf("ClusterPermission roles = %#v, want a role in namespace %q", roles, want)
}

func assertClusterPermissionRoleContains(t *testing.T, object *unstructured.Unstructured, namespace, apiGroup, resource, verb string) {
	t.Helper()
	roles, ok, err := unstructured.NestedSlice(object.Object, "spec", "roles")
	if err != nil || !ok {
		t.Fatalf("ClusterPermission roles missing: %v", err)
	}
	for _, item := range roles {
		role, _ := item.(map[string]interface{})
		if role["namespace"] != namespace {
			continue
		}
		roleRules, _, _ := unstructured.NestedSlice(role, "rules")
		for _, ruleItem := range roleRules {
			rule, _ := ruleItem.(map[string]interface{})
			if containsString(rule["apiGroups"], apiGroup) && containsString(rule["resources"], resource) && containsString(rule["verbs"], verb) {
				return
			}
		}
	}
	t.Fatalf("ClusterPermission role %q does not include %s %s/%s", namespace, verb, apiGroup, resource)
}

func assertClusterPermissionContains(t *testing.T, object *unstructured.Unstructured, apiGroup, resource, verb string) {
	t.Helper()
	if clusterPermissionContains(object, "clusterRole", apiGroup, resource, verb) ||
		clusterPermissionContains(object, "roles", apiGroup, resource, verb) {
		return
	}
	t.Fatalf("ClusterPermission does not include %s %s/%s", verb, apiGroup, resource)
}

func assertClusterPermissionDoesNotContain(t *testing.T, object *unstructured.Unstructured, apiGroup, resource, verb string) {
	t.Helper()
	if clusterPermissionContains(object, "clusterRole", apiGroup, resource, verb) ||
		clusterPermissionContains(object, "roles", apiGroup, resource, verb) {
		t.Fatalf("ClusterPermission includes unexpected %s %s/%s", verb, apiGroup, resource)
	}
}

func clusterPermissionContains(object *unstructured.Unstructured, field, apiGroup, resource, verb string) bool {
	values, ok, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", field)
	if !ok {
		return false
	}

	var rules []interface{}
	switch typed := values.(type) {
	case map[string]interface{}:
		rules, _, _ = unstructured.NestedSlice(typed, "rules")
	case []interface{}:
		for _, item := range typed {
			role, _ := item.(map[string]interface{})
			roleRules, _, _ := unstructured.NestedSlice(role, "rules")
			rules = append(rules, roleRules...)
		}
	}
	for _, item := range rules {
		rule, _ := item.(map[string]interface{})
		if containsString(rule["apiGroups"], apiGroup) && containsString(rule["resources"], resource) && containsString(rule["verbs"], verb) {
			return true
		}
	}
	return false
}

func assertAWSRoleServiceAccount(t *testing.T, role *unstructured.Unstructured, namespace, name string) {
	t.Helper()
	saNamespace, _, _ := unstructured.NestedString(role.Object, "spec", "serviceAccount", "namespace")
	saName, _, _ := unstructured.NestedString(role.Object, "spec", "serviceAccount", "name")
	if saNamespace != namespace || saName != name {
		t.Fatalf("AWSServiceAccountRole serviceAccount = %s/%s, want %s/%s", saNamespace, saName, namespace, name)
	}
}

func policyStatementAllows(role *unstructured.Unstructured, action, resource string) bool {
	statements, ok, _ := unstructured.NestedSlice(role.Object, "spec", "policyDocument", "Statement")
	if !ok {
		return false
	}
	for _, item := range statements {
		statement, _ := item.(map[string]interface{})
		if containsString(statement["Action"], action) && containsString(statement["Resource"], resource) {
			return true
		}
	}
	return false
}

func containsString(value interface{}, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []interface{}:
		for _, item := range typed {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			if item == want {
				return true
			}
		}
	}
	return false
}
