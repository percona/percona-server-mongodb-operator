#!/bin/bash
sed=$(which gsed || which sed)

NAMESPACE=$(kubectl get psmdb -o jsonpath='{.items[0].metadata.namespace}')

CA_BUNDLE=$(kubectl get secret -o jsonpath='{.data.ca-bundle}' webhook-certs)

$sed 's/caBundle: default/caBundle: '"${CA_BUNDLE}"'/g' deploy/validationWebhook.yaml | $sed 's/namespace: default/namespace: '"${NAMESPACE}"'/g' | kubectl apply -f -
