.. _howto_ldap:

How to integrate |operator| with OpenLDAP
====================================================================================

LDAP services provided by software like OpenLDAP, Microsoft Active Directory, etc. are widely used by enterprises to control information about users, systems, networks, services and applications and the corresponding access rights for the authentication/authorization process in a centralized way.

The following guide covers a simple integration of the already-installed OpenLDAP server with Percona Distribution for MongoDB and the Operator. You can know more about LDAP concepts and `LDIF <https://en.wikipedia.org/wiki/LDAP_Data_Interchange_Format>`_ files used to configure it, and find how to install and configure OpenLDAP in the official `OpenLDAP <https://www.openldap.org/doc/admin26/>`_ and `Percona Server for MongoDB <https://docs.percona.com/percona-server-for-mongodb/latest/authentication.html>`_ documentation.

The OpenLDAP side
-----------------

You can add needed OpenLDAP settings will the following LDIF portions:

.. code:: ldif

   0-percona-ous.ldif: |-
     dn: ou=perconadba,dc=ldap,dc=local
     objectClass: organizationalUnit
     ou: perconadba
   1-percona-users.ldif: |-
     dn: uid=percona,ou=perconadba,dc=ldap,dc=local
     objectClass: top
     objectClass: account
     objectClass: posixAccount
     objectClass: shadowAccount
     cn: percona
     uid: percona
     uidNumber: 1100
     gidNumber: 100
     homeDirectory: /home/percona
     loginShell: /bin/bash
     gecos: percona
     userPassword: {crypt}x
     shadowLastChange: -1
     shadowMax: -1
     shadowWarning: -1

Also a read-only user should be created for database-issued user lookups.
If everything is done correctly, the following command should work

.. code:: bash

   $ ldappasswd -s percona -D "cn=admin,dc=ldap,dc=local" -w password -x "uid=percona,ou=perconadba,dc=ldap,dc=local"


The MongoDB and Operator side
-----------------------------

In order to get MongoDB connected with OpenLDAP we need to configure both:

* Mongod
* Internal mongodb role

As for mongod you may use the following code snippet:

.. code:: yaml

   security:
     authorization: "enabled"
     ldap:
       authz:
         queryTemplate: 'ou=perconadba,dc=ldap,dc=local??sub?(&(objectClass=group)(uid={USER}))'
       servers: "openldap"
       transportSecurity: none
       bind:
         queryUser: "cn=readonly,dc=ldap,dc=local"
         queryPassword: "password"
       userToDNMapping:
         '[
             {
               match : "(.+)",
               ldapQuery: "OU=perconadba,DC=ldap,DC=local??sub?(uid={0})"
             }
      ]'
   setParameter:
     authenticationMechanisms: 'PLAIN,SCRAM-SHA-1'%

This fragment provides mongod with LDAP-specific parameters, such as FQDN of the LDAP server (``server``), explicit lookup user, domain rules, etc.

Put the snippet on you local machine and create a Kubernetes Secret object named based on :ref:`your MongoDB cluster name<cluster-name>`.

.. code:: bash

   $ kubectl create secret generic my-cluster-name-rs0-mongod --from-file=mongod.conf=<path-to-mongod-ldap-configuration>

Next step is to start the MongoDB cluster up as it’s described in :ref:`operator.kubernetes`. On successful completion of the steps from this doc, we are to proceed with setting the LDAP user roles inside the MongoDB. For this, log into MongoDB as administrator and execute the following:

.. code:: json

   var admin = db.getSiblingDB("admin")
   admin.createRole(
     {
       role: "ou=perconadba,dc=ldap,dc=local",
       privileges: [],
       roles: [ "userAdminAnyDatabase" ]
     }
   )

Now the new ``percona`` user created inside OpenLDAP is able to login to MongoDB as administrator. You can check this with the following command:

.. code:: bash

  $ mongo --username percona --password 'percona' --authenticationMechanism 'PLAIN' --authenticationDatabase '$external' --host <mongodb-rs-endpoint> --port 27017
