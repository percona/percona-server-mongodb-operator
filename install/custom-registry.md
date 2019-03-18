Use docker images from a custom registry
===================================================

Using images from a private Docker registry may be useful in different situations: it may be related to storing images inside of a company, for privacy and security reasons, etc. In such cases, Percona Server for MongoDB Operator allows to use a custom registry, and the following instruction illustrates how this can be done by the example of the Operator deployed in the OpenShift environment.

1. First of all login to the OpenShift and create project.

    ```bash
    $ oc login
    Authentication required for https://192.168.1.100:8443 (openshift)
    Username: admin
    Password:
    Login successful.
    $ oc new-project psmdb
    Now using project "psmdb" on server "https://192.168.1.100:8443".
   ```

2. There are two things you will need to configure your custom registry access:

    * the token for your user
    * your registry IP address.
    
    The token can be find out with the following command:
    
    ```bash
    $ oc whoami -t 
    ADO8CqCDappWR4hxjfDqwijEHei31yXAvWg61Jg210s
    ```
    
    And the following one tells you the registry IP address: 
    
    ```bash
    $ kubectl get services/docker-registry -n default
    NAME              TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)    AGE
    docker-registry   ClusterIP   172.30.162.173   <none>        5000/TCP   1d
    ```

3. Now you can use the obtained token and address to login to the registry:

    ```bash
    $ docker login -u admin -p ADO8CqCDappWR4hxjfDqwijEHei31yXAvWg61Jg210s 172.30.162.173:5000
    Login Succeeded
    ```

4. Pull the needed image by its SHA digest:

    ```bash
    $ docker pull docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a
    Trying to pull repository docker.io/perconalab/percona-server-mongodb-operator ...
    sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a: Pulling from docker.io/perconalab/percona-server-mongodb-operator
    Digest: sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a
    Status: Image is up to date for docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a
    ```

5. The following way is used to push an image to the custom registry (into the OpenShift psmdb project):

    ```bash
    $ docker tag \
        docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a \
        172.30.162.173:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6
    $ docker push 172.30.162.173:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6
    ```

6. Check the image in the OpenShift registry with the following command:

    ```bash
    $ oc get is
    NAME                              DOCKER REPO                                                             TAGS             UPDATED
    percona-server-mongodb-operator   docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator  0.2.1-mongod3.6  2 hours ago
    ```

7. When the custom registry image is Ok, put a Docker Repo + Tag string (it should look like `docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6`) into the `image:` option in `deploy/operator.yaml` configuration file. 

   Please note it is possible to specify `imagePullSecrets` option for all images, if the registry requires authentication.

8. Repeat steps 3-5 for other images, and update corresponding options in the `deploy/cr.yaml` file.

9. Now follow the standard [Percona Server for MongoDB Operator installation instruction](./openshift).

## Percona certified images

Following table presents Percona's certified images to be used with the Percona Server for MongoDB Operator:

### 0.2.1-mongod3.6

| Image                                                       | Digest                                                           |
|-------------------------------------------------------------|------------------------------------------------------------------|
| perconalab/percona-server-mongodb-operator:0.2.1-mongod3.6  | 69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a |


### 0.1.0

| Image                                             | Digest                                                           |
|---------------------------------------------------|------------------------------------------------------------------|
| perconalab/percona-server-mongodb-operator:0.1.0  | 7728e707ae3f6094fc101e3a3b973a854b030028e0b4fadab5a66f33cb271a54 |
