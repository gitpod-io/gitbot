#!/bin/bash

set -ex

cd config/prow
for i in $(ls *.yaml); do kubectl apply -f $i; done
cd -

kubectl create configmap plugins --from-file=plugins.yaml=config/plugins.yaml --dry-run=client -o yaml | kubectl replace configmap plugins -f -
kubectl create configmap config --from-file=config.yaml=config/config.yaml --dry-run=client -o yaml | kubectl replace configmap config -f -
