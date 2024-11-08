set -e

release="1.18.0"
local_repo="perconalab"
operator="percona-server-mongodb-operator"
package="percona-server-mongodb-operator"

#lint
yamllint -d '{extends: default, rules: {line-length: disable, indentation: disable}}' .

#build bundle
docker build --platform=linux/amd64 . -f Dockerfile -t ${local_repo}/${operator}:${release}-bundle
docker push ${local_repo}/${operator}:${release}-bundle

#validate bundle
operator-sdk bundle validate ${local_repo}/${operator}:${release}-bundle --select-optional suite=operatorframework
operator-sdk bundle validate ${local_repo}/${operator}:${release}-bundle --select-optional name=community

# #create catalog
# mkdir -p test-catalog
# if [ ! -e "test-catalog.Dockerfile" ]; then
#   darwin-amd64-opm generate dockerfile test-catalog
# fi

# cat <<EOF | tee template.yaml | darwin-amd64-opm alpha render-template semver -o yaml > test-catalog/catalog.yaml
# Schema: olm.semver
# GenerateMajorChannels: true
# GenerateMinorChannels: false
# Stable:
#   Bundles:
#     - Image: docker.io/${local_repo}/${operator}:${release}-bundle
# EOF

# #validate catalog
# darwin-amd64-opm validate test-catalog

# #build catalog
# docker build . -f test-catalog.Dockerfile -t $local_repo/test-catalog:latest --platform=linux/amd64
# docker push $local_repo/test-catalog:latest

# #create CatalogSource resource with catalog image
# CATALOG_SOURCE=$(cat <<EOF
# apiVersion: operators.coreos.com/v1alpha1
# kind: CatalogSource
# metadata:
#   name: $operator
#   namespace: openshift-marketplace
# spec:
#   displayName: Test-Operators
#   updateStrategy:
#     registryPoll:
#       interval: 5m
#   image: docker.io/$local_repo/test-catalog:latest
#   publisher: Red Hat Partner
#   sourceType: grpc
# EOF
# )
# echo "$CATALOG_SOURCE" > catalog-source.yaml

# #deploy catalog
# #oc create -f catalog-source.yaml