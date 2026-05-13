package addon

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

var clusterPermissionGVK = schema.GroupVersionKind{
	Group:   "rbac.open-cluster-management.io",
	Version: "v1alpha1",
	Kind:    "ClusterPermission",
}

func newApplierTestHarness(t *testing.T) (dynamic.Interface, *PrerequisiteApplier, map[schema.GroupVersionResource]cache.Store, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	listKinds := map[schema.GroupVersionResource]string{
		managedServiceAccountsGVR: "ManagedServiceAccountList",
		clusterPermissionsGVR:     "ClusterPermissionList",
		awsServiceAccountRolesGVR: "AWSServiceAccountRoleList",
	}
	scheme := runtime.NewScheme()
	for gvr, listKind := range listKinds {
		kind := listKind[:len(listKind)-len("List")]
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(listKind), &unstructured.UnstructuredList{})
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	informers := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	listers := map[schema.GroupVersionResource]cache.GenericLister{}
	stores := map[schema.GroupVersionResource]cache.Store{}
	for gvr := range listKinds {
		gi := informers.ForResource(gvr)
		listers[gvr] = gi.Lister()
		stores[gvr] = gi.Informer().GetStore()
	}

	var rv int64
	client.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patchAction.GetPatch()); err != nil {
			return true, nil, err
		}
		obj.SetResourceVersion(fmt.Sprintf("%d", atomic.AddInt64(&rv, 1)))
		if store, ok := stores[action.GetResource()]; ok {
			_ = store.Add(obj)
		}
		return true, obj, nil
	})

	return client, NewPrerequisiteApplier(client, listers), stores, client
}

func newClusterPermissionRequired() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "rbac.open-cluster-management.io/v1alpha1",
		"kind":       "ClusterPermission",
		"metadata": map[string]interface{}{
			"name":      "karpenter-provider-aws",
			"namespace": "target-a",
			"labels": map[string]interface{}{
				OwnerLabelKey:         OwnerLabelValue,
				TargetClusterLabelKey: "target-a",
			},
			"ownerReferences": []interface{}{
				map[string]interface{}{
					"apiVersion": "addon.open-cluster-management.io/v1beta1",
					"kind":       "ManagedClusterAddOn",
					"name":       AddonName,
					"uid":        "abc-123",
					"controller": true,
				},
			},
		},
		"spec": map[string]interface{}{
			"clusterRole": map[string]interface{}{
				"rules": []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"nodes"},
						"verbs":     []interface{}{"get", "list"},
					},
				},
			},
		},
	}}
	obj.SetGroupVersionKind(clusterPermissionGVK)
	return obj
}

func countPatches(client *dynamicfake.FakeDynamicClient) int {
	n := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "patch" {
			n++
		}
	}
	return n
}

func TestPrereqApplier_InitialApplyIssuesOnePatch(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	if err := applier.Apply(context.Background(), newClusterPermissionRequired()); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch on initial apply, got %d", got)
	}
}

func TestPrereqApplier_RepeatedApplyIsNoOp(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	required := newClusterPermissionRequired()
	if err := applier.Apply(context.Background(), required); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}
	client.ClearActions()
	if err := applier.Apply(context.Background(), required); err != nil {
		t.Fatalf("second Apply err = %v", err)
	}
	if got := countPatches(client); got != 0 {
		t.Fatalf("expected 0 patches on repeated apply, got %d", got)
	}
}

func TestPrereqApplier_SpecChangeTriggersApply(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	first := newClusterPermissionRequired()
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}
	client.ClearActions()

	changed := newClusterPermissionRequired()
	rules := changed.Object["spec"].(map[string]interface{})["clusterRole"].(map[string]interface{})["rules"].([]interface{})
	rules = append(rules, map[string]interface{}{
		"apiGroups": []interface{}{""},
		"resources": []interface{}{"pods"},
		"verbs":     []interface{}{"get"},
	})
	changed.Object["spec"].(map[string]interface{})["clusterRole"].(map[string]interface{})["rules"] = rules
	if err := applier.Apply(context.Background(), changed); err != nil {
		t.Fatalf("changed Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch after spec change, got %d", got)
	}
}

func TestPrereqApplier_LabelChangeTriggersApply(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	first := newClusterPermissionRequired()
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}
	client.ClearActions()

	changed := newClusterPermissionRequired()
	labels := changed.GetLabels()
	labels["custom"] = "new-label"
	changed.SetLabels(labels)
	if err := applier.Apply(context.Background(), changed); err != nil {
		t.Fatalf("changed Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch after label change, got %d", got)
	}
}

func TestPrereqApplier_AnnotationChangeTriggersApply(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	first := newClusterPermissionRequired()
	first.SetAnnotations(map[string]string{"x": "1"})
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}
	client.ClearActions()

	changed := newClusterPermissionRequired()
	changed.SetAnnotations(map[string]string{"x": "2"})
	if err := applier.Apply(context.Background(), changed); err != nil {
		t.Fatalf("changed Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch after annotation change, got %d", got)
	}
}

func TestPrereqApplier_OwnerReferencesChangeTriggersApply(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	first := newClusterPermissionRequired()
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}
	client.ClearActions()

	changed := newClusterPermissionRequired()
	changed.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "addon.open-cluster-management.io/v1beta1",
		Kind:       "ManagedClusterAddOn",
		Name:       AddonName,
		UID:        "different-uid",
		Controller: ptrBool(true),
	}})
	if err := applier.Apply(context.Background(), changed); err != nil {
		t.Fatalf("changed Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch after owner-ref change, got %d", got)
	}
}

func ptrBool(b bool) *bool { return &b }

func TestPrereqApplier_StatusOnlyDriftDoesNotReApply(t *testing.T) {
	_, applier, stores, client := newApplierTestHarness(t)
	first := newClusterPermissionRequired()
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply err = %v", err)
	}

	// Simulate a status-only mutation: bump status field and resourceVersion
	// without touching spec/labels/annotations/ownerRefs.
	stored := stores[clusterPermissionsGVR].List()
	if len(stored) != 1 {
		t.Fatalf("expected 1 object in store, got %d", len(stored))
	}
	live := stored[0].(*unstructured.Unstructured)
	live.Object["status"] = map[string]interface{}{"conditions": []interface{}{
		map[string]interface{}{"type": "Applied", "status": "True"},
	}}
	live.SetResourceVersion("99")
	if err := stores[clusterPermissionsGVR].Update(live); err != nil {
		t.Fatalf("update store: %v", err)
	}

	client.ClearActions()
	if err := applier.Apply(context.Background(), newClusterPermissionRequired()); err != nil {
		t.Fatalf("third Apply err = %v", err)
	}
	if got := countPatches(client); got != 0 {
		t.Fatalf("expected 0 patches on status-only drift, got %d", got)
	}
}

func TestPrereqApplier_ListerNotFoundForcesApply(t *testing.T) {
	_, applier, _, client := newApplierTestHarness(t)
	// Lister starts empty — first Apply must hit Apply path.
	if err := applier.Apply(context.Background(), newClusterPermissionRequired()); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if got := countPatches(client); got != 1 {
		t.Fatalf("expected 1 patch when lister has nothing, got %d", got)
	}
}

func TestPrereqApplier_CoexistingForeignFieldDoesNotReApply(t *testing.T) {
	// Regression: another controller may add fields we don't own.
	// DeepDerivative must treat required ⊆ existing as a match so we don't
	// trample co-owned state nor spin in a re-apply loop.
	_, applier, stores, client := newApplierTestHarness(t)

	// Seed the lister with an object that already contains every required
	// field plus an extra spec field added by a hypothetical foreign controller.
	live := newClusterPermissionRequired()
	live.Object["spec"].(map[string]interface{})["foreignField"] = "added-by-someone-else"
	live.SetResourceVersion("100")
	if err := stores[clusterPermissionsGVR].Add(live); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if err := applier.Apply(context.Background(), newClusterPermissionRequired()); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if got := countPatches(client); got != 0 {
		t.Fatalf("expected 0 patches when required ⊆ existing, got %d", got)
	}
}
