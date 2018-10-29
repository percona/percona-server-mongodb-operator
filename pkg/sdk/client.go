package sdk

import (
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"k8s.io/apimachinery/pkg/types"
)

type SDKClient struct{}

func NewClient() *SDKClient {
	return &SDKClient{}
}

func (c *SDKClient) Create(object opSdk.Object) error {
	return opSdk.Create(object)
}

func (c *SDKClient) Patch(object opSdk.Object, pt types.PatchType, patch []byte) error {
	return opSdk.Patch(object, pt, patch)
}

func (c *SDKClient) Update(object opSdk.Object) error {
	return opSdk.Update(object)
}

func (c *SDKClient) Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error {
	return opSdk.Delete(object, opts...)
}

func (c *SDKClient) Get(into opSdk.Object, opts ...opSdk.GetOption) error {
	return opSdk.Get(into, opts...)
}

func (c *SDKClient) List(namespace string, into opSdk.Object, opts ...opSdk.ListOption) error {
	return opSdk.List(namespace, into, opts...)
}

func (c *SDKClient) Handle(handler Handler) {
	return opSdk.Handle(handler)
}
