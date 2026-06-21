package addon

import (
	"context"
	"fmt"
	"strings"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	permissionv1alpha1 "open-cluster-management.io/cluster-permission/api/v1alpha1"
	msav1beta1 "open-cluster-management.io/managed-serviceaccount/apis/authentication/v1beta1"
)

var (
	managedServiceAccountsGVR = msav1beta1.GroupVersion.WithResource("managedserviceaccounts")
	clusterPermissionsGVR     = permissionv1alpha1.GroupVersion.WithResource("clusterpermissions")
	awsServiceAccountRolesGVR = schema.GroupVersionResource{Group: "aws.identity.appthrust.io", Version: "v1alpha1", Resource: "awsserviceaccountroles"}

	prerequisiteGVRs = map[string]schema.GroupVersionResource{
		"ManagedServiceAccount": managedServiceAccountsGVR,
		"ClusterPermission":     clusterPermissionsGVR,
		"AWSServiceAccountRole": awsServiceAccountRolesGVR,
	}

	prerequisiteGVRList = []schema.GroupVersionResource{
		managedServiceAccountsGVR,
		clusterPermissionsGVR,
		awsServiceAccountRolesGVR,
	}
)

// PrerequisiteGVRs returns the GroupVersionResources of every prerequisite
// object kind this package emits. Callers use this to wire dynamic informers.
func PrerequisiteGVRs() []schema.GroupVersionResource {
	out := make([]schema.GroupVersionResource, len(prerequisiteGVRList))
	copy(out, prerequisiteGVRList)
	return out
}

func BuildPrerequisiteObjects(
	managedClusterName string,
	managedClusterAddOn *addonv1beta1.ManagedClusterAddOn,
	config DeploymentConfigValues,
	managedServiceAccountAddOn *addonv1beta1.ManagedClusterAddOn,
) ([]*unstructured.Unstructured, error) {
	if strings.TrimSpace(config.AgentInstallNamespace) == "" {
		return nil, fmt.Errorf("ManagedClusterAddOn %s/%s requires AddOnDeploymentConfig spec.agentInstallNamespace", managedClusterAddOn.Namespace, managedClusterAddOn.Name)
	}
	if strings.TrimSpace(config.NodeRoleARN) == "" {
		return nil, fmt.Errorf("ManagedClusterAddOn %s/%s requires AddOnDeploymentConfig customizedVariable %s", managedClusterAddOn.Namespace, managedClusterAddOn.Name, CustomizedVariableNodeRoleARN)
	}

	owner := addonOwnerReference(managedClusterAddOn)
	if !IsHostedMode(managedClusterAddOn) {
		subject := rbacv1.Subject{
			Kind:      "ServiceAccount",
			Name:      ServiceAccountName,
			Namespace: config.AgentInstallNamespace,
		}
		permission, err := toUnstructured(clusterPermission(managedClusterName, config.AgentInstallNamespace, subject, "", false, owner))
		if err != nil {
			return nil, err
		}
		role, err := awsServiceAccountRole(managedClusterName, config.AgentInstallNamespace, ServiceAccountName, config.NodeRoleARN, owner)
		if err != nil {
			return nil, err
		}
		return []*unstructured.Unstructured{permission, role}, nil
	}

	msaInstallNamespace := ManagedServiceAccountInstallNamespace(managedServiceAccountAddOn)
	subject := rbacv1.Subject{
		APIGroup: msav1beta1.GroupVersion.Group,
		Kind:     "ManagedServiceAccount",
		Name:     AddonName,
	}
	msa, err := toUnstructured(managedServiceAccount(managedClusterName, owner))
	if err != nil {
		return nil, err
	}
	permission, err := toUnstructured(clusterPermission(managedClusterName, config.AgentInstallNamespace, subject, msaInstallNamespace, true, owner))
	if err != nil {
		return nil, err
	}
	role, err := awsServiceAccountRole(managedClusterName, msaInstallNamespace, ServiceAccountName, config.NodeRoleARN, owner)
	if err != nil {
		return nil, err
	}
	return []*unstructured.Unstructured{msa, permission, role}, nil
}

func ApplyPrerequisiteObjects(
	ctx context.Context,
	applier *PrerequisiteApplier,
	managedClusterName string,
	managedClusterAddOn *addonv1beta1.ManagedClusterAddOn,
	config DeploymentConfigValues,
	managedServiceAccountAddOn *addonv1beta1.ManagedClusterAddOn,
) error {
	objects, err := BuildPrerequisiteObjects(managedClusterName, managedClusterAddOn, config, managedServiceAccountAddOn)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := applier.Apply(ctx, object); err != nil {
			return err
		}
	}
	return nil
}

func IsHostedMode(managedClusterAddOn *addonv1beta1.ManagedClusterAddOn) bool {
	return strings.TrimSpace(managedClusterAddOn.GetAnnotations()[addonv1beta1.HostingClusterNameAnnotationKey]) != ""
}

func ManagedServiceAccountInstallNamespace(managedServiceAccountAddOn *addonv1beta1.ManagedClusterAddOn) string {
	if managedServiceAccountAddOn != nil {
		if ns := strings.TrimSpace(managedServiceAccountAddOn.Status.Namespace); ns != "" {
			return ns
		}
		if ns := strings.TrimSpace(managedServiceAccountAddOn.GetAnnotations()[addonv1beta1.InstallNamespaceAnnotation]); ns != "" {
			return ns
		}
	}
	return DefaultManagedServiceAccountInstallNamespace
}

func managedServiceAccount(namespace string, owner metav1.OwnerReference) *msav1beta1.ManagedServiceAccount {
	return &msav1beta1.ManagedServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "authentication.open-cluster-management.io/v1beta1",
			Kind:       "ManagedServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            AddonName,
			Namespace:       namespace,
			Labels:          ManagedClusterLabels(namespace),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: msav1beta1.ManagedServiceAccountSpec{
			Rotation: msav1beta1.ManagedServiceAccountRotation{
				Enabled:  true,
				Validity: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
}

func clusterPermission(
	namespace string,
	agentInstallNamespace string,
	subject rbacv1.Subject,
	sidecarNamespace string,
	includeSidecarRules bool,
	owner metav1.OwnerReference,
) *permissionv1alpha1.ClusterPermission {
	roleRef := rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     AddonName,
	}
	clusterRules := karpenterClusterRules()
	roles := []permissionv1alpha1.Role{
		{Namespace: agentInstallNamespace, Rules: karpenterControllerRoleRules()},
		{Namespace: KarpenterDNSRoleNamespace, Rules: karpenterDNSRoleRules()},
	}
	roleBindings := []permissionv1alpha1.RoleBinding{
		newRoleBinding(agentInstallNamespace, subject, AddonName),
		newRoleBinding(KarpenterDNSRoleNamespace, subject, AddonName),
	}
	if includeSidecarRules {
		clusterRules = append(clusterRules, sidecarClusterRules()...)
		roles = append(roles, permissionv1alpha1.Role{Namespace: sidecarNamespace, Rules: sidecarRoleRules()})
		roleBindings = append(roleBindings, newRoleBinding(sidecarNamespace, subject, AddonName))
	}

	return &permissionv1alpha1.ClusterPermission{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.open-cluster-management.io/v1alpha1",
			Kind:       "ClusterPermission",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            AddonName,
			Namespace:       namespace,
			Labels:          ManagedClusterLabels(namespace),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: permissionv1alpha1.ClusterPermissionSpec{
			ClusterRole: &permissionv1alpha1.ClusterRole{Rules: clusterRules},
			ClusterRoleBinding: &permissionv1alpha1.ClusterRoleBinding{
				Subject: subject,
				RoleRef: &roleRef,
			},
			Roles:        &roles,
			RoleBindings: &roleBindings,
		},
	}
}

func newRoleBinding(namespace string, subject rbacv1.Subject, roleName string) permissionv1alpha1.RoleBinding {
	return permissionv1alpha1.RoleBinding{
		Namespace: namespace,
		Subject:   subject,
		RoleRef: permissionv1alpha1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     roleName,
		},
	}
}

func awsServiceAccountRole(namespace, serviceAccountNamespace, serviceAccountName, nodeRoleARN string, owner metav1.OwnerReference) (*unstructured.Unstructured, error) {
	ownerRef, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&owner)
	if err != nil {
		return nil, fmt.Errorf("convert owner reference: %w", err)
	}
	labels := make(map[string]interface{}, 2)
	for k, v := range ManagedClusterLabels(namespace) {
		labels[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": awsServiceAccountRolesGVR.GroupVersion().String(),
		"kind":       "AWSServiceAccountRole",
		"metadata": map[string]interface{}{
			"name":            AddonName,
			"namespace":       namespace,
			"labels":          labels,
			"ownerReferences": []interface{}{ownerRef},
		},
		"spec": map[string]interface{}{
			"serviceAccount": map[string]interface{}{
				"namespace": serviceAccountNamespace,
				"name":      serviceAccountName,
			},
			"policyDocument": karpenterPolicyDocument(nodeRoleARN),
		},
	}}, nil
}

func sidecarClusterRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"selfsubjectreviews"}, Verbs: []string{"create"}},
	}
}

func sidecarRoleRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"serviceaccounts"},
			ResourceNames: []string{ServiceAccountName},
			Verbs:         []string{"get"},
		},
		{
			APIGroups:     []string{""},
			Resources:     []string{"serviceaccounts/token"},
			ResourceNames: []string{ServiceAccountName},
			Verbs:         []string{"create"},
		},
	}
}

func karpenterPolicyDocument(nodeRoleARN string) map[string]interface{} {
	return map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []interface{}{
			map[string]interface{}{
				"Sid":    "KarpenterEC2ReadWrite",
				"Effect": "Allow",
				"Action": []interface{}{
					"ec2:CreateFleet",
					"ec2:CreateLaunchTemplate",
					"ec2:CreateTags",
					"ec2:DeleteLaunchTemplate",
					"ec2:Describe*",
					"ec2:RunInstances",
					"ec2:TerminateInstances",
					"pricing:GetProducts",
					"ssm:GetParameter",
				},
				"Resource": "*",
			},
			map[string]interface{}{
				"Sid":      "KarpenterPassNodeRole",
				"Effect":   "Allow",
				"Action":   []interface{}{"iam:PassRole"},
				"Resource": []interface{}{nodeRoleARN},
			},
			map[string]interface{}{
				"Sid":    "KarpenterInstanceProfile",
				"Effect": "Allow",
				"Action": []interface{}{
					"iam:AddRoleToInstanceProfile",
					"iam:CreateInstanceProfile",
					"iam:DeleteInstanceProfile",
					"iam:GetInstanceProfile",
					"iam:RemoveRoleFromInstanceProfile",
					"iam:TagInstanceProfile",
				},
				"Resource": "*",
			},
			map[string]interface{}{
				"Sid":    "KarpenterInterruptionQueue",
				"Effect": "Allow",
				"Action": []interface{}{
					"sqs:DeleteMessage",
					"sqs:GetQueueAttributes",
					"sqs:GetQueueUrl",
					"sqs:ReceiveMessage",
				},
				"Resource": "*",
			},
		},
	}
}

func ManagedClusterLabels(managedClusterName string) map[string]string {
	return map[string]string{
		OwnerLabelKey:         OwnerLabelValue,
		TargetClusterLabelKey: managedClusterName,
	}
}

func addonOwnerReference(managedClusterAddOn *addonv1beta1.ManagedClusterAddOn) metav1.OwnerReference {
	return *metav1.NewControllerRef(managedClusterAddOn, addonv1beta1.SchemeGroupVersion.WithKind("ManagedClusterAddOn"))
}

func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	out, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert %T to unstructured: %w", obj, err)
	}
	return &unstructured.Unstructured{Object: out}, nil
}
