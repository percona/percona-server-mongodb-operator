package sdk

import (
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/types"
)

func logFields(object opSdk.Object) logrus.Fields {
	return logrus.Fields{
		"kind": object.GetObjectKind(),
	}
}

// SDKClient represents the operator-sdk client
type SDKClient struct{}

// NewClient returns a new SDKClient
func NewClient() *SDKClient {
	return &SDKClient{}
}

// Create wraps the operator-sdk Create function
func (c *SDKClient) Create(object opSdk.Object) error {
	logrus.WithFields(logFields(object)).Debugf("sending 'Create' via operator-sdk")
	return opSdk.Create(object)
}

// Patch wraps the operator-sdk Patch function
func (c *SDKClient) Patch(object opSdk.Object, pt types.PatchType, patch []byte) error {
	logrus.WithFields(logFields(object)).Debugf("sending 'Patch' via operator-sdk")
	return opSdk.Patch(object, pt, patch)
}

// Update wraps the operator-sdk Update function
func (c *SDKClient) Update(object opSdk.Object) error {
	logrus.WithFields(logFields(object)).Debugf("sending 'Update' via operator-sdk")
	return opSdk.Update(object)
}

// Delete wraps the operator-sdk Delete function
func (c *SDKClient) Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error {
	logrus.WithFields(logFields(object)).Debugf("sending 'Delete' via operator-sdk")
	return opSdk.Delete(object, opts...)
}

// Get wraps the operator-sdk Get function
func (c *SDKClient) Get(into opSdk.Object, opts ...opSdk.GetOption) error {
	logrus.WithFields(logFields(into)).Debugf("sending 'Get' via operator-sdk")
	return opSdk.Get(into, opts...)
}

// List wraps the operator-sdk List function
func (c *SDKClient) List(namespace string, into opSdk.Object, opts ...opSdk.ListOption) error {
	logrus.WithFields(logFields(into)).Debugf("sending 'List' via operator-sdk")
	return opSdk.List(namespace, into, opts...)
}
