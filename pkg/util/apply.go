package util

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ApplyStatus string

const (
	ApplyStatusCreated   ApplyStatus = "created"
	ApplyStatusUpdated   ApplyStatus = "updated"
	ApplyStatusUnchanged ApplyStatus = "unchanged"
)

func Apply(ctx context.Context, cl client.Client, obj client.Object) (ApplyStatus, error) {
	return ApplyIf(ctx, cl, obj, nil)
}

// ApplyIf works like Apply but skips the update when canWrite rejects the object stored in the cluster.
// The check runs on the object read for the write, so an object created concurrently is never overwritten.
func ApplyIf(ctx context.Context, cl client.Client, obj client.Object, canWrite func(client.Object) bool) (ApplyStatus, error) {
	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(make(map[string]string))
	}

	objAnnotations := obj.GetAnnotations()
	delete(objAnnotations, "percona.com/last-config-hash")
	obj.SetAnnotations(objAnnotations)

	hash, err := getObjectHash(obj)
	if err != nil {
		return "", errors.Wrap(err, "calculate object hash")
	}

	objAnnotations = obj.GetAnnotations()
	objAnnotations["percona.com/last-config-hash"] = hash
	obj.SetAnnotations(objAnnotations)

	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Pointer {
		val = reflect.Indirect(val)
	}
	oldObject := reflect.New(val.Type()).Interface().(client.Object)

	nn := types.NamespacedName{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	err = cl.Get(ctx, nn, oldObject)
	if err != nil && !k8serrors.IsNotFound(err) {
		return "", errors.Wrap(err, "get object")
	}

	if k8serrors.IsNotFound(err) {
		err := cl.Create(ctx, obj)
		if err == nil || !k8serrors.IsAlreadyExists(err) {
			return ApplyStatusCreated, err
		}

		// Someone created the object between the get and the create: re-read it and treat it as an update.
		if err := cl.Get(ctx, nn, oldObject); err != nil {
			return "", errors.Wrap(err, "get object")
		}
	}

	if canWrite != nil && !canWrite(oldObject) {
		return ApplyStatusUnchanged, nil
	}

	if oldObject.GetAnnotations()["percona.com/last-config-hash"] != hash ||
		!MapEqual(oldObject.GetLabels(), obj.GetLabels()) ||
		!MapEqual(oldObject.GetAnnotations(), obj.GetAnnotations()) {
		obj.SetResourceVersion(oldObject.GetResourceVersion())
		switch object := obj.(type) {
		case *corev1.Service:
			object.Spec.ClusterIP = oldObject.(*corev1.Service).Spec.ClusterIP
			object.Finalizers = oldObject.GetFinalizers()
		}

		return ApplyStatusUpdated, cl.Update(ctx, obj)
	}

	return ApplyStatusUnchanged, nil
}

func getObjectHash(obj client.Object) (string, error) {
	var dataToMarshall any
	switch object := obj.(type) {
	case *appsv1.StatefulSet:
		dataToMarshall = object.Spec
	case *appsv1.Deployment:
		dataToMarshall = object.Spec
	case *corev1.Service:
		dataToMarshall = object.Spec
	case *corev1.Secret:
		dataToMarshall = object.Data
	case *cm.Certificate:
		dataToMarshall = object.Spec
	case *cm.Issuer:
		dataToMarshall = object.Spec
	case *cm.ClusterIssuer:
		dataToMarshall = object.Spec
	default:
		dataToMarshall = obj
	}
	data, err := json.Marshal(dataToMarshall)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
