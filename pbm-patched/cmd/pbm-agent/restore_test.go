package main

import (
	"runtime"
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/config"
)

func TestGetNumInsertionWorkersConfig(t *testing.T) {
	type args struct {
		rInsWorkers *int32
		cfg         *config.RestoreConf
	}

	rZeroInsWorkers := int32(0)
	rValidInsWorkers := int32(99)

	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "When no command line param and no Restore config, return default value",
			args: args{
				rInsWorkers: nil,
				cfg:         nil,
			},
			want: numInsertionWorkersDefault,
		},
		{
			name: "When no command line param and no Restore.NumInsertionWorkers config, return default value",
			args: args{
				rInsWorkers: nil,
				cfg:         &config.RestoreConf{},
			},
			want: numInsertionWorkersDefault,
		},
		{
			name: "When zero command line param, return default value",
			args: args{
				rInsWorkers: &rZeroInsWorkers,
				cfg:         &config.RestoreConf{},
			},
			want: numInsertionWorkersDefault,
		},
		{
			name: "NumInsertionWorkers passed from commandline",
			args: args{
				rInsWorkers: &rValidInsWorkers,
				cfg:         nil,
			},
			want: 99,
		},
		{
			name: "NumInsertionWorkers passed from config",
			args: args{
				rInsWorkers: nil,
				cfg:         &config.RestoreConf{NumInsertionWorkers: 42},
			},
			want: 42,
		},
		{
			name: "NumInsertionWorkers passed from command line and from config, return from command line value",
			args: args{
				rInsWorkers: &rValidInsWorkers,
				cfg:         &config.RestoreConf{NumInsertionWorkers: 42},
			},
			want: 99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getNumInsertionWorkersConfig(tt.args.rInsWorkers, tt.args.cfg); got != tt.want {
				t.Errorf("getNumInsertionWorkersConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetNumParallelCollsConfig(t *testing.T) {
	type args struct {
		rParallelColls *int32
		restoreConf    *config.RestoreConf
	}

	rZeroParallelColls := int32(0)
	rValidParallelColls := int32(99)
	defaultValue := max(runtime.NumCPU()/2, 1)

	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "When no command line param and no Restore config, return default value",
			args: args{
				rParallelColls: nil,
				restoreConf:    nil,
			},
			want: defaultValue,
		},
		{
			name: "When no command line param and no Restore.NumInsertionWorkers config, return default value",
			args: args{
				rParallelColls: nil,
				restoreConf:    &config.RestoreConf{},
			},
			want: defaultValue,
		},
		{
			name: "When zero command line param, return default value",
			args: args{
				rParallelColls: &rZeroParallelColls,
				restoreConf:    &config.RestoreConf{},
			},
			want: defaultValue,
		},
		{
			name: "NumInsertionWorkers passed from commandline",
			args: args{
				rParallelColls: &rValidParallelColls,
				restoreConf:    nil,
			},
			want: 99,
		},
		{
			name: "NumInsertionWorkers passed from config",
			args: args{
				rParallelColls: nil,
				restoreConf:    &config.RestoreConf{NumParallelCollections: 42},
			},
			want: 42,
		},
		{
			name: "NumInsertionWorkers passed from command line and from config, return from command line value",
			args: args{
				rParallelColls: &rValidParallelColls,
				restoreConf:    &config.RestoreConf{NumParallelCollections: 42},
			},
			want: 99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getNumParallelCollsConfig(tt.args.rParallelColls, tt.args.restoreConf); got != tt.want {
				t.Errorf("getNumParallelCollsConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}
