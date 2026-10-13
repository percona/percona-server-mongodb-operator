package versionservice

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func Test_majorUpgradeRequested(t *testing.T) {
	tests := map[string]struct {
		cr         *api.PerconaServerMongoDB
		fcv        string
		want       upgradeRequest
		wantErrMsg string
	}{
		"empty mongo version in status": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2-recommended",
					},
				},
			},
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},
		"lower mongo version": {
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
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},
		"lower mongo version and only version in apply": {
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
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
			},
		},
		"same mongo version": {
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
			fcv:  "4.2",
			want: upgradeRequest{},
		},
		"too low mongo version": {
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
			fcv:        "3.6",
			wantErrMsg: "can't upgrade to 4.2 with FCV set to 3.6",
		},
		"invalid version in apply": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.0.-4.0-recommended",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "4.0.3",
				},
			},
			fcv:        "4.0",
			wantErrMsg: "parse version 4.0.-4.0-recommended from spec.upgradeOptions.apply",
		},
		"invalid version in status": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2-recommended",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "not-a-version",
				},
			},
			fcv:        "4.0",
			wantErrMsg: "parse version not-a-version from status.mongoVersion",
		},
		"recommended version in apply field": {
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
			fcv:  "3.6",
			want: upgradeRequest{},
		},
		"latest version in apply field": {
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
			fcv:  "3.6",
			want: upgradeRequest{},
		},
		"empty apply field": {
			cr:   &api.PerconaServerMongoDB{},
			want: upgradeRequest{},
		},
		"exact version in apply field": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2.1-17",
					},
				},
			},
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},
		"exact version in apply field and non-empty version in mongo status": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2.1-17",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "4.0.2-13",
				},
			},
			fcv: "4.0",
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},
		"invalid downgrade with exact version in apply": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "3.6",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "4.0.3",
				},
			},
			fcv:        "4.0",
			wantErrMsg: "can't upgrade to 3.6 with FCV set to 4.0",
		},
		"invalid downgrade with postfix version in apply": {
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
			fcv:        "4.0",
			wantErrMsg: "can't upgrade to 3.6 with FCV set to 4.0",
		},
		"valid downgrade with exact version in apply field": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2.13-14",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "4.4.1-17",
				},
			},
			fcv: "4.2",
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2.13-14",
			},
		},
		"valid downgrade with postfix version in apply field": {
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: "4.2-latest",
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					MongoVersion: "4.4.1-17",
				},
			},
			fcv: "4.2",
			want: upgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "latest",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := majorUpgradeRequested(tc.cr, tc.fcv)
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
