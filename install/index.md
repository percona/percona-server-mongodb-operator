Requirements and limitations
-------------------------------------------

The operator was developed and/or tested for the following configurations only:

1. Percona Server for MongoDB 3.6

2. OpenShift 3.11

Other options may or may not work.

Also, current PSMDB  on Kubernetes implementation is subject to the following restrictions:

1. Currently, we use DNS SRV record to connect a client application with MongoDB replica set, so client should be able to get Pod address from it and establish the direct connection to this address (when client application resides in the same Kubernetes cluster, this works by default).

   **Note:** *Kubernetes redeploys pods to another machine with a different IP address in case of failure, and [DNS SRV connection](https://docs.mongodb.com/manual/reference/connection-string/#connections-dns-seedlist) saves you from updating URI with this new IP address on all mongo clients.*

2. PSMDB backups and sharding are not yet supported.
