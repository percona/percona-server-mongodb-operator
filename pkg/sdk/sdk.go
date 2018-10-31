package sdk

import (
	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"k8s.io/apimachinery/pkg/types"
)

// Client is an interface representing an operator-sdk client,
// this is needed to mock the operator-sdk client in tests.
type Client interface {
	Create(object opSdk.Object) error
	Patch(object opSdk.Object, pt types.PatchType, patch []byte) error
	Update(object opSdk.Object) error
	Delete(object opSdk.Object, opts ...opSdk.DeleteOption) error
	Get(into opSdk.Object, opts ...opSdk.GetOption) error
	List(namespace string, into opSdk.Object, opts ...opSdk.ListOption) error
}
