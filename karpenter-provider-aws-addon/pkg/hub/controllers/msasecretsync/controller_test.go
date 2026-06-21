package msasecretsync

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	addonfake "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	addonlisterv1beta1 "open-cluster-management.io/api/client/addon/listers/addon/v1beta1"
	clusterlisterv1 "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workfake "open-cluster-management.io/api/client/work/clientset/versioned/fake"
	worklisterv1 "open-cluster-management.io/api/client/work/listers/work/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	workapplier "open-cluster-management.io/sdk-go/pkg/apis/work/v1/applier"
	"open-cluster-management.io/sdk-go/pkg/basecontroller/events"
	"open-cluster-management.io/sdk-go/pkg/basecontroller/factory"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
)

type fakeSyncContext struct {
	queue    workqueue.TypedRateLimitingInterface[string]
	recorder events.Recorder
}

func (f *fakeSyncContext) Queue() workqueue.TypedRateLimitingInterface[string] {
	return f.queue
}

func (f *fakeSyncContext) Recorder() events.Recorder {
	return f.recorder
}

func newFakeSyncContext() factory.SyncContext {
	return &fakeSyncContext{
		queue:    workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		recorder: events.NewContextualLoggingEventRecorder("test"),
	}
}

func TestSyncDefaultModeSkipsFinalizerTokenSecretAndDeletesHostedProjectionWork(t *testing.T) {
	ctx := context.Background()
	mca := validManagedClusterAddOn()
	delete(mca.Annotations, addonv1beta1.HostingClusterNameAnnotationKey)
	mca.Status.ConfigReferences = validConfigReferences()
	existingWork := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManifestWorkName("target-a"),
			Namespace: "hosting-a",
			Labels: map[string]string{
				addon.OwnerLabelKey:         addon.OwnerLabelValue,
				addon.TargetClusterLabelKey: "target-a",
			},
		},
	}
	c, fakes := newTestController([]*addonv1beta1.ManagedClusterAddOn{mca}, nil, existingWork)
	if err := c.deploymentConfigIndexer.Add(validAddOnDeploymentConfig()); err != nil {
		t.Fatalf("add deployment config: %v", err)
	}

	if err := c.sync(ctx, newFakeSyncContext(), "target-a"); err != nil {
		t.Fatalf("sync() error = %v", err)
	}

	for _, action := range fakes.addonClient.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "managedclusteraddons" {
			t.Fatalf("default mode should not add or remove the MSA sync finalizer, got action %#v", action)
		}
	}

	_, err := fakes.workClient.WorkV1().ManifestWorks("hosting-a").Get(ctx, existingWork.Name, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("default mode should delete existing hosted managed-kubeconfig ManifestWork, got err %v", err)
	}
}

func TestSyncIsNoOpOnRepeatedReconcileWhenWorkUnchanged(t *testing.T) {
	ctx := context.Background()
	mca := validManagedClusterAddOn()
	mca.Status.ConfigReferences = validConfigReferences()
	cluster := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "target-a"},
		Spec: clusterv1.ManagedClusterSpec{
			ManagedClusterClientConfigs: []clusterv1.ClientConfig{{
				URL:      "https://target-a.example.com",
				CABundle: []byte("dummy-ca"),
			}},
		},
	}
	c, fakes := newTestController([]*addonv1beta1.ManagedClusterAddOn{mca}, []runtime.Object{cluster})
	if err := c.deploymentConfigIndexer.Add(validAddOnDeploymentConfig()); err != nil {
		t.Fatalf("add deployment config: %v", err)
	}
	if err := c.secretIndexer.Add(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: addon.AddonName, Namespace: "target-a"},
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey:  []byte("token"),
			corev1.ServiceAccountRootCAKey: []byte("ca"),
		},
	}); err != nil {
		t.Fatalf("add secret: %v", err)
	}

	syncCtx := newFakeSyncContext()
	if err := c.sync(ctx, syncCtx, "target-a"); err != nil {
		t.Fatalf("first sync() error = %v", err)
	}

	// Replay informer state: the applier reads via lister, so the indexer must contain the created work.
	createdWork, err := fakes.workClient.WorkV1().ManifestWorks("hosting-a").Get(ctx, ManifestWorkName("target-a"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected work to be created on first sync, got err %v", err)
	}
	if err := fakes.workIndexer.Add(createdWork); err != nil {
		t.Fatalf("seed work indexer: %v", err)
	}

	fakes.workClient.ClearActions()
	if err := c.sync(ctx, syncCtx, "target-a"); err != nil {
		t.Fatalf("second sync() error = %v", err)
	}
	for _, action := range fakes.workClient.Actions() {
		verb := action.GetVerb()
		if verb == "patch" || verb == "create" || verb == "update" {
			t.Fatalf("second sync should not touch ManifestWork API, got action %#v", action)
		}
	}
}

func TestSyncRequiresAddOnDeploymentConfigReference(t *testing.T) {
	c, _ := newTestController([]*addonv1beta1.ManagedClusterAddOn{validManagedClusterAddOn()}, nil)

	err := c.sync(context.Background(), newFakeSyncContext(), "target-a")
	if err == nil || !strings.Contains(err.Error(), "addondeploymentconfigs") {
		t.Fatalf("sync() error = %v, want missing addondeploymentconfigs reference", err)
	}
}

func TestSyncIsNoOpOnRepeatedReconcileWhenPrereqsUnchanged(t *testing.T) {
	ctx := context.Background()
	mca := validManagedClusterAddOn()
	mca.Status.ConfigReferences = validConfigReferences()
	cluster := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "target-a"},
		Spec: clusterv1.ManagedClusterSpec{
			ManagedClusterClientConfigs: []clusterv1.ClientConfig{{
				URL:      "https://target-a.example.com",
				CABundle: []byte("dummy-ca"),
			}},
		},
	}
	c, fakes := newTestController([]*addonv1beta1.ManagedClusterAddOn{mca}, []runtime.Object{cluster})
	if err := c.deploymentConfigIndexer.Add(validAddOnDeploymentConfig()); err != nil {
		t.Fatalf("add deployment config: %v", err)
	}
	if err := c.secretIndexer.Add(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: addon.AddonName, Namespace: "target-a"},
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey:  []byte("token"),
			corev1.ServiceAccountRootCAKey: []byte("ca"),
		},
	}); err != nil {
		t.Fatalf("add secret: %v", err)
	}

	if err := c.sync(ctx, newFakeSyncContext(), "target-a"); err != nil {
		t.Fatalf("first sync() error = %v", err)
	}

	// The fake patch reactor mirrors Apply results into prereqStores, so
	// after the first sync the listers are populated. Replay them now.
	fakes.dynamicClient.ClearActions()
	if err := c.sync(ctx, newFakeSyncContext(), "target-a"); err != nil {
		t.Fatalf("second sync() error = %v", err)
	}
	for _, action := range fakes.dynamicClient.Actions() {
		if action.GetVerb() == "patch" || action.GetVerb() == "create" || action.GetVerb() == "update" {
			t.Fatalf("second sync should not re-apply unchanged prereq, got action %#v", action)
		}
	}
}

type testFakes struct {
	addonClient   *addonfake.Clientset
	workClient    *workfake.Clientset
	workIndexer   cache.Indexer
	dynamicClient *dynamicfake.FakeDynamicClient
	prereqStores  map[schema.GroupVersionResource]cache.Store
}

// prereqListKinds maps each prerequisite GVR to its list Kind, used by the
// fake dynamic client to satisfy List requests issued by dynamic informers.
var prereqListKinds = map[schema.GroupVersionResource]string{
	{Group: "authentication.open-cluster-management.io", Version: "v1beta1", Resource: "managedserviceaccounts"}: "ManagedServiceAccountList",
	{Group: "rbac.open-cluster-management.io", Version: "v1alpha1", Resource: "clusterpermissions"}:              "ClusterPermissionList",
	{Group: "aws.identity.appthrust.io", Version: "v1alpha1", Resource: "awsserviceaccountroles"}:                "AWSServiceAccountRoleList",
}

func newPrereqScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	for gvr, listKind := range prereqListKinds {
		kind := listKind[:len(listKind)-len("List")]
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(listKind), &unstructured.UnstructuredList{})
	}
	return scheme
}

func newTestController(addons []*addonv1beta1.ManagedClusterAddOn, clusters []runtime.Object, works ...runtime.Object) (*controller, testFakes) {
	addonIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, a := range addons {
		if err := addonIndexer.Add(a); err != nil {
			panic(err)
		}
	}
	clusterIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, cluster := range clusters {
		if err := clusterIndexer.Add(cluster); err != nil {
			panic(err)
		}
	}
	workIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, manifestWorkIndexers())
	for _, w := range works {
		if err := workIndexer.Add(w); err != nil {
			panic(err)
		}
	}

	addonObjects := make([]runtime.Object, 0, len(addons))
	for _, a := range addons {
		addonObjects = append(addonObjects, a)
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(newPrereqScheme(), prereqListKinds)
	dynamicInformers := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)
	prereqListers := map[schema.GroupVersionResource]cache.GenericLister{}
	prereqStores := map[schema.GroupVersionResource]cache.Store{}
	for gvr := range prereqListKinds {
		gi := dynamicInformers.ForResource(gvr)
		prereqListers[gvr] = gi.Lister()
		prereqStores[gvr] = gi.Informer().GetStore()
	}

	var rvCounter int64
	dynamicClient.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patchAction.GetPatch()); err != nil {
			return true, nil, err
		}
		// Emit a fresh resourceVersion so the applier's (hash, observedRV)
		// cache reflects a real change between Apply calls.
		obj.SetResourceVersion(fmt.Sprintf("%d", atomic.AddInt64(&rvCounter, 1)))
		// Mirror the apply into the prereq lister store so subsequent
		// reconciles observe the live object (no informer goroutine runs in test).
		if store, ok := prereqStores[action.GetResource()]; ok {
			_ = store.Add(obj)
		}
		return true, obj, nil
	})

	addonClient := addonfake.NewSimpleClientset(addonObjects...)
	workClient := workfake.NewSimpleClientset(works...)
	workLister := worklisterv1.NewManifestWorkLister(workIndexer)

	c := &controller{
		addonClient:             addonClient,
		prereqApplier:           addon.NewPrerequisiteApplier(dynamicClient, prereqListers),
		workApplier:             workapplier.NewWorkApplierWithTypedClient(workClient, workLister),
		workIndexer:             workIndexer,
		clusterLister:           clusterlisterv1.NewManagedClusterLister(clusterIndexer),
		addonLister:             addonlisterv1beta1.NewManagedClusterAddOnLister(addonIndexer),
		deploymentConfigIndexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
		secretIndexer:           cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
	}

	return c, testFakes{
		addonClient:   addonClient,
		workClient:    workClient,
		workIndexer:   workIndexer,
		dynamicClient: dynamicClient,
		prereqStores:  prereqStores,
	}
}

func validManagedClusterAddOn() *addonv1beta1.ManagedClusterAddOn {
	return &addonv1beta1.ManagedClusterAddOn{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "addon.open-cluster-management.io/v1beta1",
			Kind:       "ManagedClusterAddOn",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      addon.AddonName,
			Namespace: "target-a",
			UID:       "12345",
			Annotations: map[string]string{
				addonv1beta1.HostingClusterNameAnnotationKey: "hosting-a",
			},
		},
	}
}

func validAddOnDeploymentConfig() *addonv1beta1.AddOnDeploymentConfig {
	return &addonv1beta1.AddOnDeploymentConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "addon.open-cluster-management.io/v1beta1",
			Kind:       "AddOnDeploymentConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      addon.AddonName,
			Namespace: "target-a",
		},
		Spec: addonv1beta1.AddOnDeploymentConfigSpec{
			AgentInstallNamespace: "karpenter-system",
			CustomizedVariables: []addonv1beta1.CustomizedVariable{
				{Name: addon.CustomizedVariableAWSRegion, Value: "us-east-1"},
				{Name: addon.CustomizedVariableKarpenterClusterName, Value: "config-eks"},
				{Name: addon.CustomizedVariableClusterEndpoint, Value: "https://config-api.example.com"},
				{Name: addon.CustomizedVariableNodeRoleARN, Value: "arn:aws:iam::123456789012:role/ConfigNodeRole"},
			},
		},
	}
}

func validConfigReferences() []addonv1beta1.ConfigReference {
	return []addonv1beta1.ConfigReference{
		{
			ConfigGroupResource: addonv1beta1.ConfigGroupResource{
				Group:    "addon.open-cluster-management.io",
				Resource: "addontemplates",
			},
			DesiredConfig: &addonv1beta1.ConfigSpecHash{
				ConfigReferent: addonv1beta1.ConfigReferent{Name: addon.DefaultAddOnTemplateName},
				SpecHash:       "template-hash",
			},
		},
		{
			ConfigGroupResource: addonv1beta1.ConfigGroupResource{
				Group:    "addon.open-cluster-management.io",
				Resource: "addondeploymentconfigs",
			},
			DesiredConfig: &addonv1beta1.ConfigSpecHash{
				ConfigReferent: addonv1beta1.ConfigReferent{Name: addon.AddonName, Namespace: "target-a"},
				SpecHash:       "deployment-config-hash",
			},
		},
	}
}
