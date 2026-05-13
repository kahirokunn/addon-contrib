package addon

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// PrerequisiteApplier server-side-applies prerequisite unstructured objects,
// skipping the Apply when the live object already satisfies the desired intent
// (equality.Semantic.DeepDerivative). DeepDerivative ("required ⊆ existing")
// rather than DeepEqual is intentional: fields co-owned by other controllers
// or users do not trigger a permanent Apply loop, yet SSA Force=true still
// reclaims any field we *do* send.
type PrerequisiteApplier struct {
	client  dynamic.Interface
	listers map[schema.GroupVersionResource]cache.GenericLister
}

func NewPrerequisiteApplier(
	client dynamic.Interface,
	listers map[schema.GroupVersionResource]cache.GenericLister,
) *PrerequisiteApplier {
	return &PrerequisiteApplier{client: client, listers: listers}
}

func (a *PrerequisiteApplier) Apply(ctx context.Context, obj *unstructured.Unstructured) error {
	gvr, ok := prerequisiteGVRs[obj.GetKind()]
	if !ok {
		return fmt.Errorf("unsupported prerequisite kind %s", obj.GetKind())
	}
	lister, ok := a.listers[gvr]
	if !ok {
		return fmt.Errorf("no lister registered for %s", gvr.String())
	}

	existing, err := getFromLister(lister, obj.GetNamespace(), obj.GetName())
	if err != nil {
		return fmt.Errorf("lookup %s %s/%s: %w", obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
	}
	if existing != nil && prereqDerivative(obj, existing) {
		return nil
	}

	applied, err := a.client.Resource(gvr).Namespace(obj.GetNamespace()).Apply(
		ctx,
		obj.GetName(),
		obj,
		metav1.ApplyOptions{FieldManager: FieldManager, Force: true},
	)
	if err != nil {
		return fmt.Errorf("apply %s %s/%s: %w", obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
	}
	klog.V(4).InfoS("applied prerequisite", "kind", obj.GetKind(), "namespace", obj.GetNamespace(), "name", obj.GetName(), "resourceVersion", applied.GetResourceVersion())
	return nil
}

func getFromLister(lister cache.GenericLister, namespace, name string) (*unstructured.Unstructured, error) {
	raw, err := lister.ByNamespace(namespace).Get(name)
	switch {
	case apierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	existing, ok := raw.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected lister object type %T", raw)
	}
	return existing, nil
}

func prereqDerivative(required, existing *unstructured.Unstructured) bool {
	if !equality.Semantic.DeepDerivative(required.Object["spec"], existing.Object["spec"]) {
		return false
	}
	if !equality.Semantic.DeepDerivative(required.GetLabels(), existing.GetLabels()) {
		return false
	}
	if !equality.Semantic.DeepDerivative(required.GetAnnotations(), existing.GetAnnotations()) {
		return false
	}
	if !equality.Semantic.DeepDerivative(required.GetOwnerReferences(), existing.GetOwnerReferences()) {
		return false
	}
	return true
}
