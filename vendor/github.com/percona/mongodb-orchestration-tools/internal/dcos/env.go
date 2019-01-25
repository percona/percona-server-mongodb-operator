// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dcos

const (
	EnvFrameworkHost    = "FRAMEWORK_HOST"
	EnvPodName          = "POD_NAME"
	EnvTaskName         = "TASK_NAME"
	EnvMesosSandbox     = "MESOS_SANDBOX"
	EnvSchedulerAPIHost = "SCHEDULER_API_HOSTNAME"
	EnvSecretsEnabled   = "SECRETS_ENABLED"

	EnvMongoDBMemoryMB                 = "MONGODB_MEM"
	EnvMongoDBPrimaryAddr              = "MONGODB_PRIMARY_ADDR"
	EnvMongoDBMongodEndpointName       = "MONGODB_MONGOD_ENDPOINT_NAME"
	EnvMongoDBChangeUserDb             = "MONGODB_CHANGE_USER_DB"
	EnvMongoDBChangeUserUsername       = "MONGODB_CHANGE_USER_USERNAME"
	EnvMongoDBChangeUserNewPassword    = "MONGODB_CHANGE_USER_NEW_PASSWORD"
	EnvMongoDBWiredTigerCacheSizeRatio = "MONGODB_STORAGE_WIREDTIGER_ENGINE_CONFIG_CACHE_SIZE_RATIO"

	EnvWatchdogMetricsListen = "WATCHDOG_METRICS_LISTEN"

	EnvPMMEnabled                    = "PMM_ENABLED"
	EnvPMMEnableQueryAnalytics       = "PMM_ENABLE_QUERY_ANALYTICS"
	EnvPMMServerAddress              = "PMM_SERVER_ADDRESS"
	EnvPMMClientName                 = "PMM_CLIENT_NAME"
	EnvPMMServerSSL                  = "PMM_SERVER_SSL"
	EnvPMMServerInsecureSSL          = "PMM_SERVER_INSECURE_SSL"
	EnvPMMMongoDBClusterName         = "PMM_MONGODB_CLUSTER_NAME"
	EnvPMMLinuxMetricsExporterPort   = "PMM_LINUX_METRICS_EXPORTER_PORT"
	EnvPMMMongoDBMetricsExporterPort = "PMM_MONGODB_METRICS_EXPORTER_PORT"

	EnvMetricsEnabled    = "DCOS_METRICS_ENABLED"
	EnvMetricsInterval   = "DCOS_METRICS_INTERVAL"
	EnvMetricsStatsdHost = "STATSD_UDP_HOST"
	EnvMetricsStatsdPort = "STATSD_UDP_PORT"
)
