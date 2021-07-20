#!/bin/bash

set -ex

export TAG=cw-dev

cp -rf ../common common
docker build -t eu.gcr.io/gitpod-core-dev/prow/projectmanager:$TAG .
rm -rf common
docker push eu.gcr.io/gitpod-core-dev/prow/projectmanager:$TAG