Exposing cluster nodes with dedicated IP addresses
==============================================================

When Kubernetes creates Pods, each Pod gets its own IP address in the internal virtual network of the cluster. But creating and destroying Pods is a dynamical process, so binding communication between Pods to specific IP addresses would cause problems when something changes over time (as a result of the cluster scaling, maintenance, etc.). 
Because of this, you should connect to Percona Server for MongoDB via Kubernetes internal DNS names in URI (e.g. `mongodb+srv://userAdmin:userAdmin123456@<cluster-name>-rs0.<namespace>.svc.cluster.local/admin?replicaSet=rs0&ssl=false`). It is strictly recommended.

Sometimes it is not possible to reach Pods via Kubernetes internal DNS names.
To make Pods of the Replica Set accessible, Percona Server for MongoDB Operator can assigns a [Kubernetes Service](https://kubernetes.io/docs/concepts/services-networking/service/) to each Pod.

This feature can be configured in the Replica Set section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file:

* set 'expose.enabled' option to 'true' to allow exposing Pods via services,
* set 'expose.exposeType' option specifying the IP address type to be used:
  * `ClusterIP` - expose the Pod's service with an internal static IP address. This variant makes MongoDB Pod only reachable from within the Kubernetes cluster.
  * `NodePort` - expose the Pod's service on each Kubernetes node’s IP address at a static port. ClusterIP service, to which the node port will be routed, is automatically created in this variant. As an advantage, the service will be reachable from outside the cluster by node address and port number, but the address will be bound to a specific Kubernetes node.
  * `LoadBalancer` - expose the Pod's service externally using a cloud provider’s load balancer. Both ClusterIP and NodePort services are automatically created in this variant. 

If this feature is enabled, URI looks like `mongodb://userAdmin:userAdmin123456@<ip1>:<port1>,<ip2>:<port2>,<ip3>:<port3>/admin?replicaSet=rs0&ssl=false`
All IP adresses should be *directly* reachable by application.
