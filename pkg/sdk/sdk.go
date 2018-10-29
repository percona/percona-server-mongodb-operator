package sdk

import (
	"context"

	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Client interface {
	Create(object opSdk.Object) error
	Patch(object opSdk.Object, pt types.PatchType, patch []byte) error
	Update(object opSdk.Object) error
	Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error
	Get(into opSdk.Object, opts ...opSdk.GetOption) error
	List(namespace string, into opSdk.Object, opts ...opSdk.ListOption)
	WithListOptions(metaListOptions *metav1.ListOptions) opSdk.ListOption
	Handle(handler opSdk.Handler)
	Run(ctx context.Context)
	ExposeMetricsPort()
}
