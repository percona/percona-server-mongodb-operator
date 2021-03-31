package perconaservermongodb

import (
	"reflect"
	"testing"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func Test_majorUpgradeRequested(t *testing.T) {
	type args struct {
		cr *api.PerconaServerMongoDB
	}
	tests := []struct {
		name    string
		args    args
		want    UpgradeRequest
		wantErr bool
	}{
		{
			name: "TestWithEmptyMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recomended",
						},
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recomended",
			},
		},
		{
			name: "TestWithLowerMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recomended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.1",
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recomended",
			},
		},
		{
			name: "TestWithLowerMongoVersionInStatusAndOnlyVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.1",
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
			},
		},
		{
			name: "TestWithSameMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recomended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.2.7",
					},
				},
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},
		{
			name: "TestWithTooLowMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recomended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.4",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "TestWithTooHighMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "3.6-recomended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.31",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "TestWithInvalidVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.0.4.0-recomended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "TestWithRecommendedVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: api.UpgradeStrategyRecommended,
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.4",
					},
				},
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},
		{
			name: "TestWithLatestVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: api.UpgradeStrategyLatest,
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.4",
					},
				},
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := majorUpgradeRequested(tt.args.cr)
			if (err != nil) != tt.wantErr {
				t.Errorf("majorUpgradeRequested() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("majorUpgradeRequested() = %v, want %v", got, tt.want)
			}
		})
	}
}
