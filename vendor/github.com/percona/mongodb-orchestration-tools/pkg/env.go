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

package pkg

const (
	// general
	EnvLogVerbose     = "LOG_VERBOSE"
	EnvServiceName    = "SERVICE_NAME"
	EnvMongoDBPort    = "MONGODB_PORT"
	EnvMongoDBReplset = "MONGODB_REPLSET"

	// backup user
	EnvMongoDBBackupUser     = "MONGODB_BACKUP_USER"
	EnvMongoDBBackupPassword = "MONGODB_BACKUP_PASSWORD"

	// clusterAdmin user
	EnvMongoDBClusterAdminUser     = "MONGODB_CLUSTER_ADMIN_USER"
	EnvMongoDBClusterAdminPassword = "MONGODB_CLUSTER_ADMIN_PASSWORD"

	// clusterMonitor user
	EnvMongoDBClusterMonitorUser     = "MONGODB_CLUSTER_MONITOR_USER"
	EnvMongoDBClusterMonitorPassword = "MONGODB_CLUSTER_MONITOR_PASSWORD"

	// userAdmin user
	EnvMongoDBUserAdminUser     = "MONGODB_USER_ADMIN_USER"
	EnvMongoDBUserAdminPassword = "MONGODB_USER_ADMIN_PASSWORD"

	// mongodb ssl
	EnvMongoDBNetSSLEnabled    = "MONGODB_NET_SSL_ENABLED"
	EnvMongoDBNetSSLInsecure   = "MONGODB_NET_SSL_INSECURE"
	EnvMongoDBNetSSLPEMKeyFile = "MONGODB_NET_SSL_PEM_KEY_FILE"
	EnvMongoDBNetSSLCAFile     = "MONGODB_NET_SSL_CA_FILE"

	// replset init
	EnvInitInitiateDelay       = "INIT_INITIATE_DELAY"
	EnvInitMaxConnectTries     = "INIT_MAX_CONNECT_TRIES"
	EnvInitMaxInitReplsetTries = "INIT_MAX_INIT_REPLSET_TRIES"
	EnvInitRetrySleep          = "INIT_RETRY_SLEEP"
)
