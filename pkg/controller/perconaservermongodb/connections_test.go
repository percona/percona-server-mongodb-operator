package perconaservermongodb

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// TestConnectionLeaks aims to cover every initialization of a connection to the MongoDB database.
// Whenever we establish a connection to the MongoDB database, the "connectionCount" variable increments,
// and every time we call `Disconnect`, this variable decrements. If at the end of each reconciliation,
// this variable is not 0, it indicates that we have not closed the connection somewhere.
func TestConnectionLeaks(t *testing.T) {
	logf.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	ctx := context.Background()

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "psmdb-mock",
			Namespace:  "psmdb",
			Generation: 1,
		},
		Spec: api.PerconaServerMongoDBSpec{
			Backup: api.BackupSpec{
				Enabled: false,
			},
			CRVersion: version.Version(),
			Image:     "percona/percona-server-mongodb:latest",
			Replsets: []*api.ReplsetSpec{
				{
					Name:       "rs0",
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				},
			},
			UpdateStrategy: api.SmartUpdateStatefulSetStrategyType,
			UpgradeOptions: api.UpgradeOptions{
				SetFCV: true,
			},
			Sharding: api.Sharding{Enabled: false},
		},
		Status: api.PerconaServerMongoDBStatus{
			MongoVersion:       "4.2",
			ObservedGeneration: 1,
			State:              api.AppStateReady,
			MongoImage:         "percona/percona-server-mongodb:4.0",
		},
	}

	tests := []struct {
		name string
		cr   *api.PerconaServerMongoDB
	}{
		{
			name: "not sharded",
			cr:   cr.DeepCopy(),
		},
		{
			name: "not sharded unmanaged",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Unmanaged = true
				cr.Spec.UpdateStrategy = appsv1.RollingUpdateStatefulSetStrategyType
			}),
		},
		{
			name: "sharded",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
				}
			}),
		},
		{
			name: "sharded unmanaged",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Unmanaged = true
				cr.Spec.UpdateStrategy = appsv1.RollingUpdateStatefulSetStrategyType
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
				}
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := tt.cr
			updatedRevision := "some-revision"

			obj := []client.Object{}
			obj = append(obj, cr,
				fakeStatefulset(cr, cr.Spec.Replsets[0], cr.Spec.Replsets[0].Size, updatedRevision, ""),
				fakeStatefulset(cr, &api.ReplsetSpec{Name: "deleted-sts"}, 0, "", ""),
			)

			rsPods := fakePodsForRS(cr, cr.Spec.Replsets[0])
			allPods := append([]client.Object{}, rsPods...)

			if cr.Spec.Sharding.Enabled {
				sts := psmdb.MongosStatefulset(cr)
				sts.Spec = psmdb.MongosStatefulsetSpec(cr, corev1.PodTemplateSpec{})
				obj = append(obj, sts)

				allPods = append(allPods, fakePodsForMongos(cr)...)

				cr := cr.DeepCopy()
				if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
					t.Fatal(err)
				}
				obj = append(obj, fakeStatefulset(cr, cr.Spec.Sharding.ConfigsvrReplSet, cr.Spec.Sharding.ConfigsvrReplSet.Size, updatedRevision, ""))
				allPods = append(allPods, fakePodsForRS(cr, cr.Spec.Sharding.ConfigsvrReplSet)...)
			}

			obj = append(obj, allPods...)

			if cr.Spec.Unmanaged {
				cr := cr.DeepCopy()
				if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
					t.Fatal(err)
				}
				obj = append(obj, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cr.Spec.Secrets.Users,
						Namespace: cr.Namespace,
					},
				})
			}

			connectionCount := new(int)

			r := buildFakeClient(obj...)
			r.mongoClientProvider = &fakeMongoClientProvider{pods: rsPods, cr: cr, connectionCount: connectionCount}
			r.serverVersion = &version.ServerVersion{Platform: version.PlatformKubernetes}
			r.crons = NewCronRegistry()

			g, gCtx := errgroup.WithContext(ctx)
			gCtx, cancel := context.WithCancel(gCtx)
			g.Go(func() error {
				return updatePodsForSmartUpdate(gCtx, r.client, cr, allPods, updatedRevision)
			})
			g.Go(func() error {
				defer cancel()
				_, err := r.Reconcile(gCtx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: cr.Namespace,
						Name:      cr.Name,
					},
				})
				if *connectionCount != 0 {
					return errors.Errorf("open connections: %d", *connectionCount)
				}
				if err != nil {
					return err
				}
				// smart update sets status to initializing
				// we need second reconcile to update status to ready
				_, err = r.Reconcile(gCtx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: cr.Namespace,
						Name:      cr.Name,
					},
				})
				if *connectionCount != 0 {
					return errors.Errorf("open connections: %d", *connectionCount)
				}
				if err != nil {
					return err
				}

				if err := updateUsersSecret(ctx, r.client, cr); err != nil {
					return err
				}

				// and third reconcile to have cr with ready status from the start
				_, err = r.Reconcile(gCtx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: cr.Namespace,
						Name:      cr.Name,
					},
				})
				if *connectionCount != 0 {
					return errors.Errorf("open connections: %d", *connectionCount)
				}
				return err
			},
			)
			if err := g.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func updateResource[T any](resource T, update func(T)) T {
	update(resource)
	return resource
}

func updateUsersSecret(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) error {
	cr = cr.DeepCopy()
	if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		return err
	}
	secret := corev1.Secret{}
	err := cl.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&secret,
	)
	if err != nil {
		return err
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data["MONGODB_CLUSTER_ADMIN_PASSWORD"] = []byte("new-password")
	return cl.Update(ctx, &secret)
}

func updatePodsForSmartUpdate(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, pods []client.Object, updatedRevision string) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		time.Sleep(time.Second)

		podList := corev1.PodList{}
		err := cl.List(ctx,
			&podList,
			&client.ListOptions{
				Namespace: cr.Namespace,
			},
		)
		if err != nil {
			return err
		}
		if len(podList.Items) < len(pods) {
			podNames := make(map[string]struct{}, len(pods))
			for _, pod := range podList.Items {
				podNames[pod.GetName()] = struct{}{}
			}
			for _, pod := range pods {
				if _, ok := podNames[pod.GetName()]; !ok {
					pod := pod.DeepCopyObject().(*corev1.Pod)
					pod.Labels["controller-revision-hash"] = updatedRevision
					pod.ResourceVersion = ""
					if err := cl.Create(ctx, pod); err != nil {
						return err
					}
				}
			}
		}
	}
}

func fakePodsForRS(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) []client.Object {
	pods := []client.Object{}
	ls := naming.RSLabels(cr, rs)

	ls[naming.LabelKubernetesComponent] = "mongod"
	if rs.Name == api.ConfigReplSetName {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}
	for i := 0; i < int(rs.Size); i++ {
		pods = append(pods, fakePod(fmt.Sprintf("%s-%s-%d", cr.Name, rs.Name, i), cr.Namespace, ls, "mongod"))
	}
	return pods
}

func fakePodsForMongos(cr *api.PerconaServerMongoDB) []client.Object {
	pods := []client.Object{}
	ls := naming.MongosLabels(cr)
	ms := cr.Spec.Sharding.Mongos
	for i := 0; i < int(ms.Size); i++ {
		pods = append(pods, fakePod(fmt.Sprintf("%s-%s-%d", cr.Name, "mongos", i), cr.Namespace, ls, "mongos"))
	}
	return pods
}

func fakePod(name, namespace string, ls map[string]string, containerName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    ls,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  containerName,
					Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{
							StartedAt: metav1.Now(),
						},
					},
				},
			},
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.ContainersReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

func fakeStatefulset(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, size int32, updateRevision string, component string) client.Object {
	ls := naming.RSLabels(cr, rs)
	ls[naming.LabelKubernetesComponent] = component
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", cr.Name, rs.Name),
			Namespace: cr.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &size,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 27017,
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			UpdateRevision: updateRevision,
		},
	}
}

type fakeMongoClientProvider struct {
	pods            []client.Object
	cr              *api.PerconaServerMongoDB
	connectionCount *int
}

func (g *fakeMongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	*g.connectionCount++

	fakeClient := mongoFake.NewClient()
	return &fakeMongoClient{pods: g.pods, cr: g.cr, connectionCount: g.connectionCount, Client: fakeClient}, nil
}

func (g *fakeMongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	*g.connectionCount++

	fakeClient := mongoFake.NewClient()
	return &fakeMongoClient{pods: g.pods, cr: g.cr, connectionCount: g.connectionCount, Client: fakeClient}, nil
}

func (g *fakeMongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool) (mongo.Client, error) {
	*g.connectionCount++

	fakeClient := mongoFake.NewClient()
	return &fakeMongoClient{pods: g.pods, cr: g.cr, connectionCount: g.connectionCount, Client: fakeClient, host: host}, nil
}

type fakeMongoClient struct {
	pods            []client.Object
	cr              *api.PerconaServerMongoDB
	connectionCount *int
	host            string
	mongo.Client
}

func (c *fakeMongoClient) Disconnect(ctx context.Context) error {
	*c.connectionCount--
	return nil
}

func (c *fakeMongoClient) GetFCV(ctx context.Context) (string, error) {
	return "4.0", nil
}

func (c *fakeMongoClient) GetRole(ctx context.Context, db, role string) (*mongo.Role, error) {
	return &mongo.Role{
		Role: string(api.RoleClusterAdmin),
	}, nil
}

func (c *fakeMongoClient) GetUserInfo(ctx context.Context, username, db string) (*mongo.User, error) {
	return &mongo.User{
		Roles: []mongo.Role{},
	}, nil
}

func (c *fakeMongoClient) RSBuildInfo(ctx context.Context) (mongo.BuildInfo, error) {
	return mongo.BuildInfo{
		Version: "4.2",
		OKResponse: mongo.OKResponse{
			OK: 1,
		},
	}, nil
}

func (c *fakeMongoClient) RSStatus(ctx context.Context) (mongo.Status, error) {
	cr := c.cr.DeepCopy()
	if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		return mongo.Status{}, err
	}
	members := []*mongo.Member{}
	for key, pod := range c.pods {
		host := psmdb.GetAddr(cr, pod.GetName(), cr.Spec.Replsets[0].Name, cr.Spec.Replsets[0].GetPort())
		state := mongo.MemberStateSecondary
		if key == 0 {
			state = mongo.MemberStatePrimary
		}
		member := mongo.Member{
			Id:    key,
			Name:  host,
			State: state,
		}
		members = append(members, &member)
	}
	return mongo.Status{
		Members: members,
		OKResponse: mongo.OKResponse{
			OK: 1,
		},
	}, nil
}

func (c *fakeMongoClient) ReadConfig(ctx context.Context) (mongo.RSConfig, error) {
	cr := c.cr.DeepCopy()
	if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		return mongo.RSConfig{}, err
	}
	members := []mongo.ConfigMember{}
	for key, pod := range c.pods {
		host := psmdb.GetAddr(cr, pod.GetName(), cr.Spec.Replsets[0].Name, cr.Spec.Replsets[0].GetPort())

		member := mongo.ConfigMember{
			ID:           key,
			Host:         host,
			BuildIndexes: true,
			Priority:     mongo.DefaultPriority,
			Votes:        mongo.DefaultVotes,
		}

		member.Tags = mongo.ReplsetTags{
			"podName":     pod.GetName(),
			"serviceName": cr.Name,
		}

		members = append(members, member)
	}
	return mongo.RSConfig{
		Members: members,
	}, nil
}

func (c *fakeMongoClient) RemoveShard(ctx context.Context, shard string) (mongo.ShardRemoveResp, error) {
	return mongo.ShardRemoveResp{
		State: mongo.ShardRemoveCompleted,
		OKResponse: mongo.OKResponse{
			OK: 1,
		},
	}, nil
}

func (c *fakeMongoClient) IsBalancerRunning(ctx context.Context) (bool, error) {
	return true, nil
}

func (c *fakeMongoClient) ListShard(ctx context.Context) (mongo.ShardList, error) {
	return mongo.ShardList{
		OKResponse: mongo.OKResponse{
			OK: 1,
		},
	}, nil
}

func (c *fakeMongoClient) IsMaster(ctx context.Context) (*mongo.IsMasterResp, error) {
	isMaster := false
	if err := c.cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		return nil, err
	}
	if c.host == psmdb.GetAddr(c.cr, c.pods[0].GetName(), c.cr.Spec.Replsets[0].Name, c.cr.Spec.Replsets[0].GetPort()) {
		isMaster = true
	}
	return &mongo.IsMasterResp{
		IsMaster: isMaster,
	}, nil
}
