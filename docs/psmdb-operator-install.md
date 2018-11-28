Install Percona server for MongoDB within the orchestration environment
==========================================================================

PSMDB Operator Requirements and limitations
-------------------------------------------

The operator was developed and/or tested for the following configurations only:

1. Percona Server for MongoDB 3.6

2. OpenShift 3.11

Other options may or may not work.

Also, current PSMDB  on Kubernetes implementation is subject to the following restrictions:

1. Currently, we use DNS SRV record to connect a client application with MongoDB replica set, so client should be able to get Pod address from it and establish the direct connection to this address (when client application resides in the same Kubernetes cluster, this works by default).

   **Note:** *Kubernetes redeploys pods to another machine with a different IP address in case of failure, and [DNS SRV connection](https://docs.mongodb.com/manual/reference/connection-string/#connections-dns-seedlist) saves you from updating URI with this new IP address on all mongo clients.*

2. PSMDB backups and sharding are not yet supported.

Install Percona server for MongoDB on Kubernetes
------------------------------------------------

1. The first thing to do is to add the 'psmdb' namespace to Kubernetes, not forgetting to set the correspondent context for further steps:

   ```bash
   $ kubectl create namespace psmdb
   $ kubectl config set-context $(kubectl config current-context) --namespace=psmdb
   ```

2. Now that’s time to add the MongoDB Users secrets to Kubernetes. They should be placed in the data section of the `deploy/mongodb-users.yaml` file as base64-encoded logins and passwords for the MongoDB specific user accounts (see https://kubernetes.io/docs/concepts/configuration/secret/ for details). After editing is finished, mongodb-users secrets should be created (or updated with the new passwords) using the following command:

   ```bash
   $ kubectl create -f deploy/mongodb-users.yaml
   ```

   More details about secrets can be found in a [separate section](./psmdb-operator.install.md#more-on-required-secrets).

   **Note:** *the following command can be used to get base64-encoded password from a plain text string:* `$ echo -n 'plain-text-password' | base64`

3. Now RBAC (role-based access control) and Custom Resource Definition for PSMDB should be created from the following two files: `deploy/rbac.yaml` and `deploy/crd.yaml`. Briefly speaking, role-based access is based on specifically defined roles and actions corresponding to them, allowed to be done on specific Kubernetes resources (details about users and roles can be found in [Kubernetes documentation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#default-roles-and-role-bindings)). Custom Resource Definition extends the standard set of resources which Kubernetes “knows” about with the new items (in our case ones which are the core of the percona-server-mongodb-operator). 

   ```bash
   $ kubectl create -f deploy/crd.yaml -f deploy/rbac.yaml
   ```

   **Note:** *This step requires your user to have cluster-admin role privileges. For example, those using Google Kubernetes Engine can grant user needed privileges with the following command:* `$ kubectl create clusterrolebinding cluster-admin-binding1 --clusterrole=cluster-admin --user=<myname@example.org>`

4. Finally it’s time to start the percona-server-mongodb-operator within Kubernetes:

   ```bash
   $ kubectl create -f deploy/operator.yaml
   ```
5. After the operator is started, Percona Server for MongoDB cluster can be created at any time with the following command:

   ```bash
   $ kubectl apply -f deploy/cr.yaml
   ```

   Creation process will take some time. The process is over when both operator and replica set pod have reached their Running status:

   ```bash
   $ kubectl get pods
   NAME                                               READY   STATUS    RESTARTS   AGE
   my-cluster-name-rs0-0                              1/1     Running   0          8m
   my-cluster-name-rs0-1                              1/1     Running   0          8m
   my-cluster-name-rs0-2                              1/1     Running   0          7m
   percona-server-mongodb-operator-754846f95d-sf6h6   1/1     Running   0          9m
   ```

7. Check connectivity to newly created cluster

   ```bash
   $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il percona-client:/$ mongo "mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
   ```

Install Percona server for MongoDB on OpenShift
-----------------------------------------------

1. The first thing to do is to create a new `psmdb` project:

   ```bash
   $ oc new-project psmdb
   ```

2. Now that’s time to add the MongoDB Users secrets to OpenShift. They should be placed in the data section of the `deploy/mongodb-users.yaml` file as base64-encoded logins and passwords for the MongoDB specific user accounts (see https://kubernetes.io/docs/concepts/configuration/secret/ for details).

   **Note:** *the following command can be used to get base64-encoded password from a plain text string:* `$ echo -n 'plain-text-password' | base64`

3. After editing is finished, mongodb-users secrets should be created (or updated with the new passwords) using the following command:

   ```bash
   $ oc create -f deploy/mongodb-users.yaml
   ```

   More details about secrets can be found in a [separate section](./psmdb-operator.install.md#more-on-required-secrets).

4. Now RBAC (role-based access control) and Custom Resource Definition for PSMDB should be created from the following two files: `deploy/rbac.yaml` and `deploy/crd.yaml`. Briefly speaking, role-based access is based on specifically defined roles and actions corresponding to them, allowed to be done on specific Kubernetes resources (details about users and roles can be found in [OpenShift documentation](https://docs.openshift.com/enterprise/3.0/architecture/additional_concepts/authorization.html)). Custom Resource Definition extends the standard set of resources which Kubernetes “knows” about with the new items (in our case ones which are the core of the percona-server-mongodb-operator).

   ```bash
   $ oc project psmdb
   $ oc apply -f deploy/crd.yaml -f deploy/rbac.yaml
   ```

   **Note:** *This step requires your user to have cluster-admin role privileges.*

5. An extra step is needed if you want to manage PSMDB cluster from a non-privileged user. Necessary permissions can be granted by applying the next clusterrole:

   ```bash
   $ oc create clusterrole psmdb-admin --verb="*" --resource=perconaservermongodbs.psmdb.percona.com
   $ oc adm policy add-cluster-role-to-user psmdb-admin <some-user>
   ```

6. Finally, it’s time to start the percona-server-mongodb-operator within OpenShift:

   ```bash
   $ oc create -f deploy/operator.yaml
   ```

7. After the operator is started, Percona Server for MongoDB cluster can be created at any time with the following two steps:

   a. Uncomment the `deploy/cr.yaml` field `#platform:` and set it to `platform: openshift`. The result should be like this:

     ```yaml
     apiVersion: psmdb.percona.com/v1alpha1
     kind: PerconaServerMongoDB
     metadata:
       name: my-cluster-name
     spec:
       platform: openshift
     ...
     ```

   b. Create/apply the CR file:

      ```bash
      $ oc apply -f deploy/cr.yaml
      ```

   Creation process will take some time. The process is over when both operator and replica set pod have reached their Running status:

   ```bash
   $ oc get pods
   NAME                                               READY   STATUS    RESTARTS   AGE
   my-cluster-name-rs0-0                              1/1     Running   0          8m
   my-cluster-name-rs0-1                              1/1     Running   0          8m
   my-cluster-name-rs0-2                              1/1     Running   0          7m
   percona-server-mongodb-operator-754846f95d-sf6h6   1/1     Running   0          9m
   ```

7. Check connectivity to newly created cluster 

   ```bash
   $ oc run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash percona-client:/$ mongo "mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?ssl=false"
   ```

More on Required Secrets
------------------------

As it was already written in the installation part, the operator requires Kubernetes Secrets to be deployed before it is started. The name of the required secrets can be set in `deploy/cr.yaml` under the `spec.secrets` section.

### MongoDB System Users

*Default Secret name:* `my-cluster-name-mongodb-users`

*Secret name field:* `spec.secrets.users`

The operator requires system-level MongoDB Users to automate the MongoDB deployment. 

**Warning:** *These users should not be used to run an application.*


|User Purpose   | Username Secret Key | Password Secret Key     | MongoDB Role                    |
|---------------|---------------------|-------------------------|---------------------------------|
|Backup/Restore | MONGODB_BACKUP_USER | MONGODB_BACKUP_PASSWORD | [backup](https://docs.mongodb.com/manual/reference/built-in-roles/#backup), [clusterMonitor](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor), [restore](https://docs.mongodb.com/manual/reference/built-in-roles/#restore) |
|Cluster Admin  | MONGODB_CLUSTER_ADMIN_USER | MONGODB_CLUSTER_ADMIN_PASSWORD | [clusterAdmin](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterAdmin)      |
|Cluster Monitor| MONGODB_CLUSTER_MONITOR_USER| MONGODB_CLUSTER_MONITOR_PASSWORD | [clusterMonitor](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor) |
|User Admin     | MONGODB_USER_ADMIN_USER    | MONGODB_USER_ADMIN_PASSWORD    | [userAdmin](https://docs.mongodb.com/manual/reference/built-in-roles/#userAdmin)         |

### Development Mode

To make development and testing easier, a secrets file with default MongoDB System User/Passwords is located at `deploy/mongodb-users.yaml`.

The development-mode credentials from `deploy/mongodb-users.yaml` are:

|Secret Key                      | Secret Value |
|--------------------------------|--------------|
|MONGODB_BACKUP_USER             | backup       |
|MONGODB_BACKUP_PASSWORD         | backup123456 |
|MONGODB_CLUSTER_ADMIN_USER      | clusterAdmin |
|MONGODB_CLUSTER_ADMIN_PASSWORD  | clusterAdmin123456  |
|MONGODB_CLUSTER_MONITOR_USER    | clusterMonitor      |
|MONGODB_CLUSTER_MONITOR_PASSWORD| clusterMonitor123456|
|MONGODB_USER_ADMIN_USER         | userAdmin       |
|MONGODB_USER_ADMIN_PASSWORD     | userAdmin123456 |

**Warning:** *Do not use the default MongoDB Users in production!*

### MongoDB Internal Authentication Key (optional)

*Default Secret name:* `my-cluster-name-mongodb-key`

*Secret name field:* `spec.secrets.key`

By default, the operator will create a random, 1024-byte key for [MongoDB Internal Authentication](https://docs.mongodb.com/manual/core/security-internal-authentication/) if it does not already exist. If you would like to deploy a different key, create the secret manually before starting the operator.
