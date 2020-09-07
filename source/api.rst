PSMDB API Documentation
=======================

.. raw:: html

   <style>
   
   .toggle {
        background: none repeat scroll 0 0 #ffebcc;
        padding: 12px;
        max-width: 850px;
        line-height: 24px;
        margin-bottom: 24px;
    }
   
   .toggle .header {
       display: block;
       clear: both;
       cursor: pointer;
   }
   
   .toggle .header:after {
       content: " ▶";
   }
   
   .toggle .header.open:after {
       content: " ▼";
   }
   </style>

Percona Operator Operator for Percona Server for MongoDB provides an `aggregation-layer extension for the Kubernetes API <https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/>`_. Please refer to the
`official Kubernetes API documentation <https://kubernetes.io/docs/reference/>`_ on the API access and usage details.
The following subsections describe the Percona XtraDB Cluster API provided by the Operator.

.. contents:: :local:

Prerequisites
-------------

1. Create the namespace name you will use, if not exist:

   .. code-block:: yaml

      kubectl create namespace my-namespace-name

   Trying to create an already-existing namespace will show you a
   self-explanatory error message. Also, you can use the ``defalut`` namespace.

   .. note:: In this document ``default`` namespace is used in all examples.
      Substitute ``default`` with your namespace name if you use a different
      one.

2. Prepare:

   .. code-block:: yaml

      # set correct API address
      KUBE_CLUSTER=$(kubectl config view --minify -o jsonpath='{.clusters[0].name}')
      API_SERVER=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"$KUBE_CLUSTER\")].cluster.server}" | sed -e 's#https://##')

      # create service account and get token
      kubectl apply -f deploy/crd.yaml -f deploy/rbac.yaml -n default
      KUBE_TOKEN=$(kubectl get secret $(kubectl get serviceaccount percona-server-mongodb-operator -o jsonpath='{.secrets[0].name}' -n default) -o jsonpath='{.data.token}' -n default | base64 --decode )


Create new PSMDB cluster
------------------------

**Description:**

.. code-block:: bash

   The command to create a new PSMDB cluster creating all of its resources and it depends on the PSMDB Operator

**Kubectl Command:**

.. code-block:: bash

   kubectl apply -f percona-server-mongodb-operator/deploy/cr.yaml

**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v{{{apiversion}}}/namespaces/default/perconaservermongodbs

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN


**cURL Request:**

.. code-block:: bash

   curl -k -v -XPOST "https://$API_SERVER/apis/psmdb.percona.com/v{{{apiversion}}}/namespaces/default/perconaservermongodbs" \
               -H "Content-Type: application/json" \
               -H "Accept: application/json" \
               -H "Authorization: Bearer $KUBE_TOKEN" \
               -d "@cluster.json"

**Request Body (cluster.json):**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-create-cluster-request-json.txt

**Inputs:**

  **Metadata**:
  
  1. Name (String, min-length: 1) : ``contains name of cluster``
  
  **Spec**:

  1. secrets[users] (String, min-length: 1) : ``contains name of secret for the users``
  2. allowUnsafeConfigurations (Boolean, Default: false) : ``allow unsafe configurations to run``
  3. image (String, min-length: 1) : ``name of the psmdb cluster image``

  replsets:
  
  1. name (String, min-length: 1) : ``name of monogo replicaset``
  2. size (Integer, min-value: 1) : ``contains size of MongoDB replicaset``
  3. expose[exposeType] (Integer, min-value: 1) : ``type of service to expose replicaset``
  4. arbiter (Object) : ``configuration for mongo arbiter``

  mongod:
  
  1. net:
  
     1. port (Integer, min-value: 0) : ``contains mongod container port``
     2. hostPort (Integer, min-value: 0) : ``host port to expose mongod on``
     
  2. security:

     1. enableEncryption (Boolean, Default: true) : ``enable encrypting mongod storage``
     2. encryptionKeySecret (String, min-length: 1) : ``name of encryption key secret``
     3. encryptionCipherMode (String, min-length: 1) : ``type of encryption cipher to use``

  3. setParameter (Object): ``configure mongod enginer paramters``
  4. storage:

     1. engine (String, min-length: 1, default "wiredTiger"): ``name of mongod storage engine``
     2. inMemory (Object) : ``wiredTiger engine configuration``
     3. wiredTiger (Object) : ``wiredTiger engine configuration``

  pmm:
  
  1. serverHost (String, min-length: 1) : ``serivce name for monitoring``
  2. image (String, min-length: 1) : ``name of pmm image``
    
  backup:
  
  1. image (String, min-length: 1) : ``name of MngoDB backup docker image``
  2. serviceAccountName (String, min-length: 1) ``name of service account to use for backup``
  3. storages (Object) : ``storage configuration object for backup``

**Response:**

.. container:: toggle

   .. container:: header

      JSON

   .. include:: ./assets/code/api-create-cluster-response-json.txt

List PSMDB cluster
------------------

**Description:**

.. code-block:: bash

   Lists all PSMDB clusters that exist in your kubernetes cluster

**Kubectl Command:**

.. code-block:: bash

   kubectl get psmdb

**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs?limit=500

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN

**cURL Request:**

.. code-block:: bash

   curl -k -v -XGET "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs?limit=500" \
               -H "Accept: application/json;as=Table;v=v1;g=meta.k8s.io,application/json;as=Table;v=v1beta1;g=meta.k8s.io,application/json" \
               -H "Authorization: Bearer $KUBE_TOKEN"

**Request Body:**

.. code-block:: bash

   None

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-list-cluster-response-json.txt

Get status of PSMDB cluster
---------------------------

**Description:**

.. code-block:: bash

   Gets all information about specified PSMDB cluster

**Kubectl Command:**

.. code-block:: bash

   kubectl get psmdb/my-cluster-name -o json

**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN

**cURL Request:**

.. code-block:: bash

   curl -k -v -XGET "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name" \
               -H "Accept: application/json" \
               -H "Authorization: Bearer $KUBE_TOKEN"

**Request Body:**

.. code-block:: bash

   None

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-get-status-of-cluster-response-json.txt

Scale up/down PSMDB cluster
---------------------------

**Description:**

.. code-block:: bash

   Increase or decrease the size of the PSMDB cluster nodes to fit the current high availability needs

**Kubectl Command:**

.. code-block:: bash

   kubectl patch psmdb my-cluster-name --type=merge --patch '{
   "spec": {"replsets":{ "size": "5" }
   }}'

**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN

**cURL Request:**

.. code-block:: bash

   curl -k -v -XPATCH "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name" \
               -H "Authorization: Bearer $KUBE_TOKEN" \
               -H "Content-Type: application/merge-patch+json" 
               -H "Accept: application/json" \
               -d '{  
                     "spec": {"replsets":{ "size": "5" }
                     }}'

**Request Body:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-scale-cluster-request-json.txt

**Input:**

   **spec**:

   replsets

   1. size (Int or String, Defaults: 3): ``Specifiy the sie of the replsets cluster to scale up or down to``

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-scale-cluster-response-json.txt

Update PSMDB cluster image
--------------------------

**Description:**

.. code-block:: bash

   Change the image of PSMDB containers inside the cluster

**Kubectl Command:**

.. code-block:: bash

   kubectl patch psmdb my-cluster-name --type=merge --patch '{  
   "spec": {"psmdb":{ "image": "percona/percona-server-mongodb-operator:1.4.0-mongod4.2" }  
   }}'

**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN


**cURL Request:**

.. code-block:: bash

   curl -k -v -XPATCH "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbs/my-cluster-name" \
               -H "Authorization: Bearer $KUBE_TOKEN" \
               -H "Accept: application/json" \
               -H "Content-Type: application/merge-patch+json" 
               -d '{  
                 "spec": {"psmdb":{ "image": "percona/percona-server-mongodb-operator:1.4.0-mongod4.2" }
                 }}'

**Request Body:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-update-cluster-image-request-json.txt

**Input:**

  **spec**:
  
  psmdb:
  
  1. image (String, min-length: 1) : ``name of the image to update for PSMDB``

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-update-cluster-image-response-json.txt

Backup PSMDB cluster
--------------------

**Description:**

.. code-block:: bash

   Takes a backup of the PSMDB cluster containers data to be able to recover from disasters or make a roll-back later


**Kubectl Command:**

.. code-block:: bash

   kubectl apply -f percona-server-mongodb-operator/deploy/backup/backup.yaml


**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbbackups


**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN


**cURL Request:**

.. code-block:: bash

   curl -k -v -XPOST "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbbackups" \
               -H "Accept: application/json" \
               -H "Content-Type: application/json" \
               -d "@backup.json" -H "Authorization: Bearer $KUBE_TOKEN"

**Request Body (backup.json):**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-backup-cluster-request-json.txt

**Input:**

1. **metadata**:

     name(String, min-length:1) : ``name of backup to create``

2. **spec**:
  
     1. psmdbCluster(String, min-length:1) : ``name of PSMDB cluster``
     2. storageName(String, min-length:1) : ``name of storage claim to use``

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-backup-cluster-response-json.txt

Restore PSMDB cluster
---------------------

**Description:**

.. code-block:: bash

   Restores PSMDB cluster data to an earlier version to recover from a problem or to make a roll-back


**Kubectl Command:**

.. code-block:: bash

   kubectl apply -f percona-server-mongodb-operator/deploy/backup/restore.yaml


**URL:**

.. code-block:: bash

   https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbrestores

**Authentication:**

.. code-block:: bash

   Authorization: Bearer $KUBE_TOKEN


**cURL Request:**

.. code-block:: bash

   curl -k -v -XPOST "https://$API_SERVER/apis/psmdb.percona.com/v1/namespaces/default/perconaservermongodbrestores" \
               -H "Accept: application/json" \
               -H "Content-Type: application/json" \
               -d "@restore.json" \
               -H "Authorization: Bearer $KUBE_TOKEN"

**Request Body (restore.json):**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-restore-cluster-request-json.txt

**Input:**

1. **metadata**:

     name(String, min-length:1): ``name of restore to create``

2. **spec**:

     1. сlusterName(String, min-length:1) : ``name of PSMDB cluster``
     2. backupName(String, min-length:1) : ``name of backup to restore from``

**Response:**

.. container:: toggle

   .. container:: header

      JSON:

   .. include:: ./assets/code/api-restore-cluster-response-json.txt

