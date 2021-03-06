package utils

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdateWithLabel is supposed to be used my MarkForCleanup routines.
// This is not supposed to be used to update the labels in CreateOrUpdate calls
func UpdateWithLabel(ctx context.Context, c client.Client, obj client.Object, key, value string) error {
	err := c.Get(ctx, types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, obj)
	if err != nil {
		return err
	}

	var labels map[string]string
	labels = obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[key] = value

	obj.SetLabels(labels)

	return c.Update(context.TODO(), obj)
}
