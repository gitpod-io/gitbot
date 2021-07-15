#!/bin/bash

set -ex

cd config/prow
for i in $(ls *.yaml); do kubectl apply -f $i; done
cd -

kubectl create configmap plugins --from-file=plugins.yaml=config/plugins.yaml --dry-run -o yaml | kubectl replace configmap plugins -f -
kubectl create configmap config --from-file=config.yaml=config/config.yaml --dry-run -o yaml | kubectl replace configmap config -f -

kubectl create configmap projectmanager --from-file=projectmanager.yaml=config/projectmanager.yaml --dry-run -o yaml | kubectl replace configmap projectmanager -f -
kubectl create configmap groundwork --from-file=groundwork.yaml=config/groundwork.yaml --dry-run -o yaml | kubectl replace configmap groundwork -f -