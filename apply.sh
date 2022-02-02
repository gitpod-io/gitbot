#!/bin/bash

set -ex

cd config/prow
for i in $(ls *.yaml); do kubectl apply -f $i; done
cd -

kubectl create configmap plugins --from-file=plugins.yaml=config/plugins.yaml --dry-run=client -o yaml | kubectl replace configmap plugins -f -
kubectl create configmap config --from-file=config.yaml=config/config.yaml --dry-run=client -o yaml | kubectl replace configmap config -f -

# note: ensure these config maps already exist
kubectl create configmap customlabels --from-file=customlabels.yaml=config/customlabels.yaml --dry-run=client -o yaml | kubectl replace configmap customlabels -f -
kubectl create configmap observer --from-file=observer.yaml=config/observer.yaml --dry-run=client -o yaml | kubectl replace configmap observer -f -
kubectl create configmap willkommen --from-file=willkommen.yaml=config/willkommen.yaml --dry-run=client -o yaml | kubectl replace configmap willkommen -f -
