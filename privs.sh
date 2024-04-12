#!/bin/bash

# Variables

kubectl_auth_command="kubectl auth can-i"  # Kubectl auth command
missing_privileges=false
cluster_wide=false                         # Set if operator is installed in cluster-wide mode
missing_resources=false                    # If resource is not installed in k8s cluster
# PXC and PSMDB for future use
pxc_operator_install=true
psmdb_operator_install=true
pg_operator_install=true

# privileges to check in the k8s cluster
privileges=(
    "create customresourcedefinitions"
    "get customresourcedefinitions"
    "list customresourcedefinitions"
    "watch customresourcedefinitions"
    "create events"
    "patch events"
    "create serviceaccount"
    "get serviceaccount"
    "list serviceaccount"
    "watch serviceaccount"            
    "'*' role"
    "'*' pods"
    "'*' pods --subresource=exec"
    "'*' pods --subresource=log"
    "'*' configmaps"
    "'*' services"
    "'*' persistentvolumeclaims"
    "'*' deployments.apps"
    "'*' replicasets.apps"
    "'*' statefulset.apps"
    "'*' jobs.batch"
    "'*' cronjobs.batch"
    "'*' poddisruptionbudgets.policy"
    "'*' leases"
    "'*' issuers"
    "'*' certificates")

# Display usage information
usage() {
    echo "Usage: $0 [--as AS_USER] [--namespace NAMESPACE] [--cluster-wide]"
    echo "  --as AS_USER      Specify the user/service account(system:serviceaccount:<namespace>:<service-account>) to impersonate for privileges check"
    echo "  --namespace NS    Specify the namespace for privileges check"
    echo "  --cluster-wide    Set to perform cluster-wide privileges check"
    echo "  --kubeconfig PATH   Specify the path to the kubeconfig file"
    exit 1
}

# Check privileges with kubectl auth can-i
check_privileges() {
    action="$1"
    command="${kubectl_auth_command} $action"
    result=$(eval "$command" 2>&1)
    if [[ "$result" != *"yes"* ]]; then
        echo "Permission denied for action: $action"
        
        # Check if missing privileges is for Postgres Operator
        if [[ ${pg_operator_privileges[@]} =~ $action ]] ; then 
	        pg_operator_install=false
            return
        fi
        # Placeholder for PXC and PSMDB for future

        # Common privileges are missing, no operator can be installed
        missing_privileges=true

    elif [[ "$result" == *"Warning: the server doesn't have a resource type"* ]]; then
        IFS=' ' read -ra action_array <<< "$action"
        RESOURCE=$(echo "$action" | cut -d ' ' -f 2-)
	    missing_resources=true
        echo "Warning: Unable to check the privileges for resource '$RESOURCE', check if the resource '$RESOURCE' is present in the cluster"	    
    fi
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    key="$1"
    case $key in
        --as)
        AS_USER="$2"
        kubectl_auth_command="${kubectl_auth_command} --as ${AS_USER}"
        shift # past argument
        shift # past value
        ;;
        -n|--namespace)
        NAMESPACE="$2"
        shift # past argument
        shift # past value
        kubectl_auth_command="${kubectl_auth_command} -n ${NAMESPACE}"
        ;;
        --kubeconfig)
        KUBE_CONFIG="$2"
        shift # past argument
        shift # past value
        kubectl_auth_command="${kubectl_auth_command} --kubeconfig ${KUBE_CONFIG}"
        ;;        
        --cluster-wide)
        cluster_wide=true
        shift # past argument
        ;;
        --help|-h)
        usage
        ;;
        *)    # unknown option
        echo "Unknown option: $1"
        exit 1
        ;;
    esac
done



# privileges for namespace-wide or cluster-wide mode
if [[ "$cluster_wide" == true ]]; then
    privileges+=(
        "create clusterrole"
        "get clusterrole"
        "list clusterrole"
        "watch clusterrole
        "create clusterrolebinding"
        "get clusterrolebinding"
        "list clusterrolebinding"
        "watch clusterrolebinding"
    )
else
    privileges+=(
        "create role"
        "get role"
        "list role"
        "watch role
        "create clusterrolebinding"
        "get clusterrolebinding"
        "list clusterrolebinding"
        "watch clusterrolebinding"
    )
fi

# privileges for PXC Operator

# privileges for PSMDB Operator

# privileges for Postgress Operator
pg_operator_privileges=(
    "'*' endpoints"
    "create endpoints --subresource=restricted"
)


# Run kubectl auth can-i commands for the privileges
printf "Checking privileges to install Percona Operators in kubernetes cluster...\n"

for perm in "${privileges[@]}"; do
    check_privileges "$perm"
done


if ${missing_resources}; then
    printf "\nWarning: Some resources are not found in the kubernetes cluster.Check the Warning messages before you proceed\n"
fi
printf '%.0s-' {1..90}
if ${missing_privileges}; then
    printf "\nMISSING PRIVILEGES TO INSTALL any Percona Operator\n"
    exit 0
fi 

if ! ${pg_operator_install}; then 
    printf "\nMISSING PRIVILEGES TO INSTALL: Percona Operator for PostgreSQL\n"
    printf "https://docs.percona.com/percona-operator-for-postgresql/index.html\n"
else
     printf "\nGOOD TO INSTALL: Percona Operator for PostgreSQL\n"
     printf "https://docs.percona.com/percona-operator-for-postgresql/index.html\n"
fi
printf '%.0s-' {1..90};

if ! ${pxc_operator_install}; then
    printf "\nMISSING PRIVILEGES TO INSTALL: Percona Operator for MySQL based on Percona XtraDB Cluster\n"
    printf "https://docs.percona.com/percona-operator-for-postgresql/index.html\n"
else
     printf "\nGOOD TO INSTALL: Percona Operator for MySQL based on Percona XtraDB Cluster\n"
     printf "https://docs.percona.com/percona-operator-for-postgresql/index.html\n"
fi
printf '%.0s-' {1..90}; 

if ! ${psmdb_operator_install}; then 
    printf "\nMISSING PRIVILEGES TO INSTALL: Percona Operator for MongoDB\n"
    printf "https://docs.percona.com/percona-operator-for-mongodb/index.html\n"
else
     printf "\nGOOD TO INSTALL: Percona Operator for MongoDB\n"
     printf "https://docs.percona.com/percona-operator-for-mongodb/index.html\n"
fi
printf '%.0s-' {1..90} ; echo ""
