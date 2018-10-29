package sdk

import (
	"context"

	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"k8s.io/apimachinery/pkg/types"
)

type Client interface {
	Create(object opSdk.Object) error
	Patch(object opSdk.Object, pt types.PatchType, patch []byte) error
	Update(object opSdk.Object) error
	Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error
	Get(into opSdk.Object, opts ...opSdk.GetOption) error
	List(namespace string, into opSdk.Object, opts ...opSdk.ListOption)
	Handle(handler Handler)
}

type Handler interface {
	Handle(ctx context.Context, event opSdk.Event) error
}
