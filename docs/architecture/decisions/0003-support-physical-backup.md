# [Support physical backup based on pbm tool]

* Date: [2022-07-11]


## Status

* Status: [proposed]


## Context

PBM tool can support physical backup from v1.7.0 while current Mongo operator can not. Considering the efficiency of physical backup for large size db, should enable this feature via Mongo operator.


## Brief procedure

* Add a type field (logical or physical) in CR PSMDB (spec.backup.type) and PSMDB-Backup to indicate backup type.
* Let pbm tool recognize and check Mongo server's encryption/compression setting so that it can handle db data correctly during restoration. If pbm found different setting of encryption/compression, it will failed with error. PBM tool can not transfer db data files from one format to another for now.
* Let pbm tool hide Mongo server's K8S entrypoint file (/data/db/ps-entry.sh) during restoration and recover entrypoint file before restoration succesful return. In this way, mongod container will stay in unready state and will not be restarted to jump in and interrupt restoration process.
* Mount Mongo server's data volume into pbm container as well.
* Mount encryption keyfile into pbm container as well if encryption enabled.
* Add mongod binary into pbm tool's image as well because pbm will exec mongod temporarily.


## Considered Options: How to make mongod container stay DOWN during restoration

* Option A: Hide mongod container's entrypoint file and after shutdown mongo server, mongod container will stay DOWN until entryfile been restored.
* Option B: Design a mechanism for pbm tool to inform psmdb operator to delete mongod container from statfulset.


## Decision

Chosen option: "option A", because it is quite straightforward. Option B will add a lot of complexity into current architecture and there is no other such requirement for pbm notifying psmdb operator. Also considering pbm tool is not intended be used in K8S only, it is not good to let pbm tool handle K8S resource directly.


## Consequences

User can trigger a physical backup or physical restore (pretty the same way with logical restore) from K8S CR (psmdb or psmdb-backup)


### Negative Consequences <!-- optional -->

* Hide container's entrypoint file seems a little hack.

