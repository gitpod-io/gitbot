#!/bin/bash

set -ex

export TAG=dev

cp -rf ../common common
docker build -t eu.gcr.io/gitpod-core-dev/prow/groundwork:$TAG .
rm -rf common
docker push eu.gcr.io/gitpod-core-dev/prow/groundwork:$TAG