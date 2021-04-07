package perconaservermongodb

import (
	"reflect"
	"testing"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func Test_majorUpgradeRequested(t *testing.T) {
	type args struct {
		cr  *api.PerconaServerMongoDB
		fcv string
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
							Apply: "4.2-recommended",
						},
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},

		{
			name: "TestWithLowerMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},

		{
			name: "TestWithLowerMongoVersionAndOnlyVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
			},
		},

		{
			name: "TestWithSameMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.2.3",
					},
				},
				fcv: "4.2",
			},
			want: UpgradeRequest{
				Ok:         true,
				Apply:      "recommended",
				NewVersion: "4.2",
			},
		},

		{
			name: "TestWithTooLowMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
			},
			wantErr: true,
		},

		{
			name: "TestWithTooHighMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "3.6-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			wantErr: true,
		},

		{
			name: "TestWithInvalidVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.0.4.0-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
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
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
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
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},

		{
			name: "TestWithExactVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2.1-17",
						},
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},

		{
			name: "TestWithExactVersionInApplyFieldAndNonEmptyVersionInMongoStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2.1-17",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.2.-13",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := majorUpgradeRequested(tt.args.cr, tt.args.fcv)
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
