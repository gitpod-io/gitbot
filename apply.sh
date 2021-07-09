#!?bin/bash

set -ex

cd prow
for i in $(ls *.yaml); do kubectl apply -f $i; done