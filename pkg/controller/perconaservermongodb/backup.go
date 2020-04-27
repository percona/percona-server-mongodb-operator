package perconaservermongodb

import (
	"context"
	"fmt"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

const (
	roleBindingName = "mongodb-operator"
)

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(cr *api.PerconaServerMongoDB) error {
	ctasks := make(map[string]struct{})
	ls := backup.NewBackupCronJobLabels(cr.Name)

	for _, task := range cr.Spec.Backup.Tasks {
		cjob := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup, cr.Spec.ImagePullSecrets)
		ls = cjob.ObjectMeta.Labels
		if task.Enabled {
			ctasks[cjob.Name] = struct{}{}

			err := setControllerReference(cr, cjob, r.scheme)
			if err != nil {
				return fmt.Errorf("set owner reference for backup task %s: %v", cjob.Name, err)
			}

			err = r.client.Create(context.TODO(), cjob)
			if err != nil && errors.IsAlreadyExists(err) {
				err := r.client.Update(context.TODO(), cjob)
				if err != nil {
					return fmt.Errorf("update task %s: %v", task.Name, err)
				}
			} else if err != nil {
				return fmt.Errorf("create task %s: %v", task.Name, err)
			}
		}
	}

	// Remove old/unused tasks
	tasksList := &batchv1b.CronJobList{}
	err := r.client.List(context.TODO(),
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
		tasksList,
	)
	if err != nil {
		return fmt.Errorf("get backup list: %v", err)
	}

	for _, t := range tasksList.Items {
		if _, ok := ctasks[t.Name]; !ok {
			err := r.client.Delete(context.TODO(), &t)
			if err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete backup task %s: %v", t.Name, err)
			}
		}
	}

	// no ns set, ignore it because we cannot run without it
	ns, err := k8sutil.GetWatchNamespace()
	if err != nil {
		return err
	}
	if cr.Namespace != ns {
		var sa corev1.ServiceAccount
		if err := r.client.Get(context.TODO(), client.ObjectKey{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Backup.ServiceAccountName,
		}, &sa); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("get service account %s: %v", cr.Spec.Backup.ServiceAccountName, err)
			}

			sa = corev1.ServiceAccount{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "ServiceAccount",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      cr.Spec.Backup.ServiceAccountName,
					Namespace: cr.Namespace,
				},
			}

			if err := setControllerReference(cr, &sa, r.scheme); err != nil {
				return fmt.Errorf("set owner reference for service account %s: %v", cr.Spec.Backup.ServiceAccountName, err)
			}

			if err := r.client.Create(context.TODO(), &sa); err != nil {
				return fmt.Errorf("create service account %s: %v", cr.Spec.Backup.ServiceAccountName, err)
			}
		}

		var rb rbacv1.RoleBinding
		if err := r.client.Get(context.TODO(), client.ObjectKey{
			Namespace: cr.Namespace,
			Name:      roleBindingName,
		}, &rb); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("get role binding %s: %v", roleBindingName, err)
			}

			rb = rbacv1.RoleBinding{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.authorization.k8s.io/v1",
					Kind:       "RoleBinding",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      roleBindingName,
					Namespace: cr.Namespace,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "mongodb-operator",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "ServiceAccount",
						Name: cr.Spec.Backup.ServiceAccountName,
					},
				},
			}

			if err := setControllerReference(cr, &rb, r.scheme); err != nil {
				return fmt.Errorf("set owner reference for role binding %s: %v", roleBindingName, err)
			}

			if err := r.client.Create(context.TODO(), &rb); err != nil {
				return fmt.Errorf("create role binding %s: %v", roleBindingName, err)
			}
		}
	}

	return nil
}
