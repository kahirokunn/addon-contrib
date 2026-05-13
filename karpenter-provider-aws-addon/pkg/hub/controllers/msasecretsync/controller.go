package msasecretsync

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformers "k8s.io/client-go/informers"
	kubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
	addonclientset "open-cluster-management.io/api/client/addon/clientset/versioned"
	addoninformers "open-cluster-management.io/api/client/addon/informers/externalversions"
	addonlisterv1beta1 "open-cluster-management.io/api/client/addon/listers/addon/v1beta1"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterinformers "open-cluster-management.io/api/client/cluster/informers/externalversions"
	clusterlisterv1 "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	workclientset "open-cluster-management.io/api/client/work/clientset/versioned"
	workinformers "open-cluster-management.io/api/client/work/informers/externalversions"
	workv1 "open-cluster-management.io/api/work/v1"
	workapplier "open-cluster-management.io/sdk-go/pkg/apis/work/v1/applier"
	"open-cluster-management.io/sdk-go/pkg/basecontroller/factory"

	"open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/pkg/addon"
	"open-cluster-management.io/ocm/pkg/common/queue"
)

const controllerName = "karpenter-msa-secret-sync"

var ownerLabelSelector = labels.SelectorFromSet(labels.Set{
	addon.OwnerLabelKey: addon.OwnerLabelValue,
}).String()

type controller struct {
	addonClient             addonclientset.Interface
	prereqApplier           *addon.PrerequisiteApplier
	workApplier             *workapplier.WorkApplier
	workIndexer             cache.Indexer
	clusterLister           clusterlisterv1.ManagedClusterLister
	addonLister             addonlisterv1beta1.ManagedClusterAddOnLister
	deploymentConfigIndexer cache.Indexer
	secretIndexer           cache.Indexer
}

type RunOptions struct {
	KubeClient    kubernetes.Interface
	AddonClient   addonclientset.Interface
	ClusterClient clusterclientset.Interface
	DynamicClient dynamic.Interface
	WorkClient    workclientset.Interface
}

func Run(ctx context.Context, opts RunOptions) error {
	kubeInformers := kubeinformers.NewSharedInformerFactoryWithOptions(
		opts.KubeClient,
		10*time.Minute,
		kubeinformers.WithTweakListOptions(func(o *metav1.ListOptions) {
			o.FieldSelector = "metadata.name=" + addon.AddonName
		}),
	)
	addonInformers := addoninformers.NewSharedInformerFactory(opts.AddonClient, 10*time.Minute)
	clusterInformers := clusterinformers.NewSharedInformerFactory(opts.ClusterClient, 10*time.Minute)
	workInformers := workinformers.NewSharedInformerFactoryWithOptions(
		opts.WorkClient,
		10*time.Minute,
		workinformers.WithTweakListOptions(func(o *metav1.ListOptions) {
			o.LabelSelector = ownerLabelSelector
		}),
	)
	workInformer := workInformers.Work().V1().ManifestWorks()
	if err := workInformer.Informer().AddIndexers(manifestWorkIndexers()); err != nil {
		return fmt.Errorf("add ManifestWork indexers: %w", err)
	}

	addonInformer := addonInformers.Addon().V1beta1().ManagedClusterAddOns().Informer()
	deploymentConfigInformer := addonInformers.Addon().V1beta1().AddOnDeploymentConfigs().Informer()
	secretInformer := kubeInformers.Core().V1().Secrets().Informer()

	dynamicInformers := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		opts.DynamicClient,
		10*time.Minute,
		metav1.NamespaceAll,
		func(o *metav1.ListOptions) {
			o.LabelSelector = ownerLabelSelector
		},
	)
	prereqGVRs := addon.PrerequisiteGVRs()
	prereqListers := make(map[schema.GroupVersionResource]cache.GenericLister, len(prereqGVRs))
	prereqInformers := make([]factory.Informer, 0, len(prereqGVRs))
	for _, gvr := range prereqGVRs {
		gi := dynamicInformers.ForResource(gvr)
		prereqListers[gvr] = gi.Lister()
		prereqInformers = append(prereqInformers, gi.Informer())
	}

	c := &controller{
		addonClient:             opts.AddonClient,
		prereqApplier:           addon.NewPrerequisiteApplier(opts.DynamicClient, prereqListers),
		workApplier:             workapplier.NewWorkApplierWithTypedClient(opts.WorkClient, workInformer.Lister()),
		workIndexer:             workInformer.Informer().GetIndexer(),
		clusterLister:           clusterInformers.Cluster().V1().ManagedClusters().Lister(),
		addonLister:             addonInformers.Addon().V1beta1().ManagedClusterAddOns().Lister(),
		deploymentConfigIndexer: deploymentConfigInformer.GetIndexer(),
		secretIndexer:           secretInformer.GetIndexer(),
	}

	ctrl := factory.New().
		WithInformersQueueKeysFunc(queue.QueueKeyByMetaNamespace, secretInformer).
		WithFilteredEventsInformersQueueKeysFunc(
			queue.QueueKeyByMetaNamespace,
			filterByNames(addon.AddonName, addon.ManagedServiceAccountAddonName),
			addonInformer,
		).
		WithFilteredEventsInformersQueueKeysFunc(
			queue.QueueKeyByMetaNamespace,
			filterByNames(addon.AddonName),
			deploymentConfigInformer,
		).
		WithInformersQueueKeysFunc(
			queue.QueueKeyByMetaNamespace,
			prereqInformers...,
		).
		WithBareInformers(clusterInformers.Cluster().V1().ManagedClusters().Informer()).
		WithSync(c.sync).
		ToController(controllerName)

	kubeInformers.Start(ctx.Done())
	addonInformers.Start(ctx.Done())
	clusterInformers.Start(ctx.Done())
	workInformers.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())

	ctrl.Run(ctx, 1)
	return nil
}

func filterByNames(names ...string) factory.EventFilterFunc {
	return func(obj interface{}) bool {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return false
		}
		name := accessor.GetName()
		for _, n := range names {
			if name == n {
				return true
			}
		}
		return false
	}
}

func (c *controller) sync(ctx context.Context, syncCtx factory.SyncContext, managedClusterName string) error {
	logger := klog.FromContext(ctx).WithValues("managedCluster", managedClusterName)
	managedClusterAddOn, err := c.addonLister.ManagedClusterAddOns(managedClusterName).Get(addon.AddonName)
	if apierrors.IsNotFound(err) {
		return c.deleteHostingWorkByTarget(ctx, managedClusterName)
	}
	if err != nil {
		return err
	}

	if !managedClusterAddOn.DeletionTimestamp.IsZero() {
		if err := c.deleteHostingWorkForAddon(ctx, managedClusterAddOn); err != nil {
			return err
		}
		return removeFinalizer(ctx, c.addonClient, managedClusterAddOn)
	}

	config, err := c.deploymentConfigValues(managedClusterAddOn)
	if err != nil {
		return err
	}

	if !addon.IsHostedMode(managedClusterAddOn) {
		if err := addon.ApplyPrerequisiteObjects(ctx, c.prereqApplier, managedClusterName, managedClusterAddOn, config, nil); err != nil {
			return err
		}
		if err := c.deleteHostingWorkForAddon(ctx, managedClusterAddOn); err != nil {
			return err
		}
		return removeFinalizer(ctx, c.addonClient, managedClusterAddOn)
	}

	managedServiceAccountAddOn, err := c.managedServiceAccountAddOn(managedClusterName)
	if err != nil {
		return err
	}
	if err := ensureFinalizer(ctx, c.addonClient, managedClusterAddOn); err != nil {
		return err
	}

	if err := addon.ApplyPrerequisiteObjects(ctx, c.prereqApplier, managedClusterName, managedClusterAddOn, config, managedServiceAccountAddOn); err != nil {
		return err
	}

	secretObj, exists, err := c.secretIndexer.GetByKey(managedClusterName + "/" + addon.AddonName)
	if err != nil {
		return err
	}
	if !exists {
		logger.Info("waiting for MSA token Secret")
		return nil
	}
	secret, ok := secretObj.(*corev1.Secret)
	if !ok {
		return fmt.Errorf("unexpected Secret cache object %T", secretObj)
	}

	managedCluster, err := c.clusterLister.Get(managedClusterName)
	if err != nil {
		return err
	}

	work, err := BuildHostingSecretManifestWork(managedClusterAddOn, managedCluster, config.AgentInstallNamespace, secret)
	if err != nil {
		return err
	}

	_, err = c.workApplier.Apply(ctx, work)
	return err
}

func (c *controller) deploymentConfigValues(managedClusterAddOn *addonv1beta1.ManagedClusterAddOn) (addon.DeploymentConfigValues, error) {
	ref, err := desiredAddOnDeploymentConfigRef(managedClusterAddOn)
	if err != nil {
		return addon.DeploymentConfigValues{}, err
	}
	namespace := ref.Namespace
	if namespace == "" {
		namespace = managedClusterAddOn.Namespace
	}
	obj, exists, err := c.deploymentConfigIndexer.GetByKey(namespace + "/" + ref.Name)
	if err != nil {
		return addon.DeploymentConfigValues{}, err
	}
	if !exists {
		return addon.DeploymentConfigValues{}, fmt.Errorf("AddOnDeploymentConfig %s/%s referenced by ManagedClusterAddOn %s/%s not found", namespace, ref.Name, managedClusterAddOn.Namespace, managedClusterAddOn.Name)
	}
	config, ok := obj.(*addonv1beta1.AddOnDeploymentConfig)
	if !ok {
		return addon.DeploymentConfigValues{}, fmt.Errorf("unexpected AddOnDeploymentConfig cache object %T", obj)
	}
	return addon.ValuesFromDeploymentConfig(managedClusterAddOn, config)
}

func desiredAddOnDeploymentConfigRef(managedClusterAddOn *addonv1beta1.ManagedClusterAddOn) (*addonv1beta1.ConfigSpecHash, error) {
	for _, ref := range managedClusterAddOn.Status.ConfigReferences {
		if ref.Group != addon.AddOnConfigGroup || ref.Resource != addon.AddOnDeploymentConfigResource {
			continue
		}
		if ref.DesiredConfig == nil || ref.DesiredConfig.Name == "" {
			return nil, fmt.Errorf("ManagedClusterAddOn %s/%s requires desiredConfig for %s/%s", managedClusterAddOn.Namespace, managedClusterAddOn.Name, addon.AddOnConfigGroup, addon.AddOnDeploymentConfigResource)
		}
		return ref.DesiredConfig, nil
	}
	return nil, fmt.Errorf("ManagedClusterAddOn %s/%s requires status.configReferences for %s/%s", managedClusterAddOn.Namespace, managedClusterAddOn.Name, addon.AddOnConfigGroup, addon.AddOnDeploymentConfigResource)
}

func (c *controller) managedServiceAccountAddOn(managedClusterName string) (*addonv1beta1.ManagedClusterAddOn, error) {
	managedServiceAccountAddOn, err := c.addonLister.ManagedClusterAddOns(managedClusterName).Get(addon.ManagedServiceAccountAddonName)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return managedServiceAccountAddOn, err
}

func (c *controller) deleteHostingWorkForAddon(ctx context.Context, mca *addonv1beta1.ManagedClusterAddOn) error {
	if hostingClusterName := mca.Annotations[addonv1beta1.HostingClusterNameAnnotationKey]; hostingClusterName != "" {
		return c.workApplier.Delete(ctx, hostingClusterName, ManifestWorkName(mca.Namespace))
	}
	return c.deleteHostingWorkByTarget(ctx, mca.Namespace)
}

func (c *controller) deleteHostingWorkByTarget(ctx context.Context, managedClusterName string) error {
	objs, err := c.workIndexer.ByIndex(indexByTargetCluster, managedClusterName)
	if err != nil {
		return err
	}
	var errs []error
	for _, obj := range objs {
		work := obj.(*workv1.ManifestWork)
		if err := c.workApplier.Delete(ctx, work.Namespace, work.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
