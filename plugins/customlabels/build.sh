#!/usr/bin/env bash

set -ex

export TAG=leo-dev

docker build -t eu.gcr.io/gitpod-core-dev/prow/customlabels:$TAG
docker push eu.gcr.io/gitpod-core-dev/prow/customlabels:$TAG