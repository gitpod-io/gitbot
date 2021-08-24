#!/usr/bin/env bash

set -ex

export TAG=dev

docker build -t eu.gcr.io/gitpod-core-dev/prow/willkommen:$TAG .

docker push eu.gcr.io/gitpod-core-dev/prow/willkommen:$TAG