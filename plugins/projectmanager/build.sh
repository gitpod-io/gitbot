#!/bin/bash

set -ex

docker build -t eu.gcr.io/gitpod-core-dev/prow/projectmanager:dev .
docker push eu.gcr.io/gitpod-core-dev/prow/projectmanager:dev