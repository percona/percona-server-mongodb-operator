package sdk

import (
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"k8s.io/apimachinery/pkg/types"
)

// SDKClient represents the operator-sdk client
type SDKClient struct{}

// NewClient returns a new SDKClient
func NewClient() *SDKClient {
	return &SDKClient{}
}

// Create wraps the operator-sdk Create function
func (c *SDKClient) Create(object opSdk.Object) error {
	return opSdk.Create(object)
}

// Patch wraps the operator-sdk Create function
func (c *SDKClient) Patch(object opSdk.Object, pt types.PatchType, patch []byte) error {
	return opSdk.Patch(object, pt, patch)
}

// Update wraps the operator-sdk Create function
func (c *SDKClient) Update(object opSdk.Object) error {
	return opSdk.Update(object)
}

// Delete wraps the operator-sdk Create function
func (c *SDKClient) Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error {
	return opSdk.Delete(object, opts...)
}

// Get wraps the operator-sdk Create function
func (c *SDKClient) Get(into opSdk.Object, opts ...opSdk.GetOption) error {
	return opSdk.Get(into, opts...)
}

// List wraps the operator-sdk Create function
func (c *SDKClient) List(namespace string, into opSdk.Object, opts ...opSdk.ListOption) error {
	return opSdk.List(namespace, into, opts...)
}
